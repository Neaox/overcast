// This file provides a stub Store used until step 3 replaces it with a
// concrete backend (likely smallstep/truststore). It applies on every GOOS.

package trust

import "go.uber.org/zap"

func newStore(_ *zap.Logger) (Store, error) { return nil, ErrUnsupported }
