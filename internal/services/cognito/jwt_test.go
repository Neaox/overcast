package cognito

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

// countingStore wraps a real store and records per-namespace Get/Set counts.
// Signing-key generation is not observable on its own — RSA key generation
// touches neither the clock nor the store — but every generation in
// getOrCreateSigningKey is immediately followed by a Set to cognito:sigkeys,
// so the Set count is the observable proxy for it. The Get count is what
// pins the validation hot path to one store read (issue #1731).
type countingStore struct {
	state.Store
	mu   sync.Mutex
	gets map[string]int
	sets map[string]int
}

func newCountingStore() *countingStore {
	return &countingStore{
		Store: state.NewMemoryStore(),
		gets:  map[string]int{},
		sets:  map[string]int{},
	}
}

func (c *countingStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	c.mu.Lock()
	c.gets[namespace]++
	c.mu.Unlock()
	return c.Store.Get(ctx, namespace, key)
}

func (c *countingStore) Set(ctx context.Context, namespace, key, value string) error {
	c.mu.Lock()
	c.sets[namespace]++
	c.mu.Unlock()
	return c.Store.Set(ctx, namespace, key, value)
}

func (c *countingStore) counts(namespace string) (gets, sets int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets[namespace], c.sets[namespace]
}

func (c *countingStore) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets = map[string]int{}
	c.sets = map[string]int{}
}

// newSigningKeyTestService builds a Cognito service over a counting in-memory
// store and a mock clock.
func newSigningKeyTestService(t *testing.T) (*Service, *countingStore, *clock.Mock) {
	t.Helper()
	st := newCountingStore()
	clk := clock.NewMock()
	s := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, st, zap.NewNop(), clk)
	return s, st, clk
}

// mintTokenForPool creates a pool, its signing key, and a signed JWT for it,
// returning the token. The store counters are reset afterwards so a test can
// measure the validation path alone.
func mintTokenForPool(t *testing.T, s *Service, st *countingStore, clk *clock.Mock, poolID string) string {
	t.Helper()
	ctx := context.Background()
	if err := s.savePool(ctx, &UserPool{ID: poolID, Name: "pool", CreatedAt: clk.Now()}); err != nil {
		t.Fatalf("savePool: %v", err)
	}
	priv, kid, err := s.getOrCreateSigningKey(ctx, poolID)
	if err != nil {
		t.Fatalf("getOrCreateSigningKey: %v", err)
	}
	token, err := signJWT(priv, kid, map[string]any{
		"iss":       "http://localhost:4566/" + poolID,
		"token_use": "access",
		"exp":       float64(clk.Now().Add(time.Hour).Unix()),
	})
	if err != nil {
		t.Fatalf("signJWT: %v", err)
	}
	st.reset()
	return token
}

// signTokenForUnknownPool signs a well-formed JWT with a key this server has
// never seen, so its issuer names a pool that does not exist.
func signTokenForUnknownPool(t *testing.T, clk *clock.Mock, poolID string) string {
	t.Helper()
	// A throwaway service with its own store mints the key, so the store under
	// test never learns about it.
	other := New(&config.Config{Region: "us-east-1"}, state.NewMemoryStore(), zap.NewNop(), clk)
	priv, kid, err := other.getOrCreateSigningKey(context.Background(), poolID)
	if err != nil {
		t.Fatalf("getOrCreateSigningKey: %v", err)
	}
	token, err := signJWT(priv, kid, map[string]any{
		"iss":       "http://attacker.example/" + poolID,
		"token_use": "access",
		"exp":       float64(clk.Now().Add(time.Hour).Unix()),
	})
	if err != nil {
		t.Fatalf("signJWT: %v", err)
	}
	return token
}

// TestValidateCognitoToken_unknownPoolMintsNoSigningKey is the regression test
// for #1731: API Gateway's COGNITO_USER_POOLS / JWT authorizers hand every
// bearer token straight to ValidateCognitoToken, so a token whose iss claim
// names a pool the caller invented used to cost one RSA-2048 generation and
// one permanent cognito:sigkeys record per distinct issuer path. Rejection was
// never in doubt; the unbounded, caller-keyed state growth was the defect.
func TestValidateCognitoToken_unknownPoolMintsNoSigningKey(t *testing.T) {
	// Given: a server with no user pools at all.
	s, st, clk := newSigningKeyTestService(t)
	ctx := context.Background()
	token := signTokenForUnknownPool(t, clk, "us-east-1_madeup")

	// When: a token whose issuer names a pool that does not exist is validated.
	_, err := s.ValidateCognitoToken(ctx, token)

	// Then: it is rejected...
	if err == nil {
		t.Fatal("ValidateCognitoToken accepted a token for a pool that does not exist")
	}

	// ...and no signing key was generated or persisted for the invented pool.
	if _, sets := st.counts(nsSigningKeys); sets != 0 {
		t.Errorf("validation wrote %d signing-key record(s), want 0", sets)
	}
	kvs, scanErr := st.Scan(ctx, nsSigningKeys, "")
	if scanErr != nil {
		t.Fatalf("scan %s: %v", nsSigningKeys, scanErr)
	}
	if len(kvs) != 0 {
		t.Errorf("%s holds %d record(s) after validating an unknown-pool token, want 0: %v",
			nsSigningKeys, len(kvs), kvs)
	}
}

// TestValidateAccessToken_unknownPoolMintsNoSigningKey covers the same defect
// on the two Cognito-native token paths. GetUser, GlobalSignOut, RevokeToken
// and friends accept a caller-supplied access token, so an invented issuer
// reaches the key lookup there without API Gateway being involved at all.
func TestValidateAccessToken_unknownPoolMintsNoSigningKey(t *testing.T) {
	cases := []struct {
		name string
		// validate returns true when the token was rejected.
		validate func(s *Service, ctx context.Context, token string) bool
	}{
		{
			name: "classic JSON path",
			validate: func(s *Service, ctx context.Context, token string) bool {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "http://localhost:4566/", nil)
				_, ok := s.validateAccessToken(ctx, w, r, token)
				return !ok
			},
		},
		{
			name: "typed path",
			validate: func(s *Service, ctx context.Context, token string) bool {
				_, aerr := s.validateAccessTokenTyped(ctx, token)
				return aerr != nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a server with no user pools at all.
			s, st, clk := newSigningKeyTestService(t)
			ctx := context.Background()
			token := signTokenForUnknownPool(t, clk, "us-east-1_madeup")

			// When: the access token is validated.
			rejected := tc.validate(s, ctx, token)

			// Then: it is rejected without persisting a signing key.
			if !rejected {
				t.Fatal("accepted an access token for a pool that does not exist")
			}
			if _, sets := st.counts(nsSigningKeys); sets != 0 {
				t.Errorf("validation wrote %d signing-key record(s), want 0", sets)
			}
		})
	}
}

// TestValidateCognitoToken_realPoolStaysOneStoreRead pins the hot path. The
// fix for #1731 must not buy its safety with an extra store read on every
// authorised request: resolving the pool through cognito:pools first would
// double the reads, so validation reads cognito:sigkeys and nothing else, and
// a missing key is itself the "no such pool" answer.
func TestValidateCognitoToken_realPoolStaysOneStoreRead(t *testing.T) {
	// Given: a real pool with a real token minted from its signing key.
	s, st, clk := newSigningKeyTestService(t)
	ctx := context.Background()
	token := mintTokenForPool(t, s, st, clk, "us-east-1_abc123")

	// When: the token is validated.
	claims, err := s.ValidateCognitoToken(ctx, token)

	// Then: it is accepted, with exactly one signing-key read and no other read.
	if err != nil {
		t.Fatalf("ValidateCognitoToken rejected a valid token: %v", err)
	}
	if iss, _ := claims["iss"].(string); !strings.HasSuffix(iss, "us-east-1_abc123") {
		t.Errorf("claims[iss] = %q, want a suffix of us-east-1_abc123", iss)
	}
	gets, sets := st.counts(nsSigningKeys)
	if gets != 1 {
		t.Errorf("validation performed %d %s read(s), want 1", gets, nsSigningKeys)
	}
	if sets != 0 {
		t.Errorf("validation performed %d %s write(s), want 0", sets, nsSigningKeys)
	}
	if poolGets, _ := st.counts(nsPools); poolGets != 0 {
		t.Errorf("validation performed %d %s read(s), want 0", poolGets, nsPools)
	}
}
