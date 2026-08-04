package rds

// password.go — changing a DB instance's master password.
//
// On AWS, ModifyDBInstance's MasterUserPassword applies to the live database:
// the old password stops working and the new one starts. Here the database is
// a container, and the password it answers to was baked into its environment
// at `docker run` time — MYSQL_ROOT_PASSWORD and friends are read once, when
// the engine initialises its data directory, and never again. Restarting the
// container with a new value therefore changes nothing, and recreating it
// would apply the password by throwing the data away.
//
// So the change is made the way a DBA would make it: by running the engine's
// own password statement inside the container, authenticated with the password
// the engine still has. That leaves exactly one thing the record can say about
// a password — that the engine accepts it — and it is why a failure here fails
// the whole modification. Storing a password the container does not honour is
// the same silence that made this parameter worth fixing: the API says the
// rotation happened and every connection using the new password is refused.
//
// Two cases have no running engine to take the change, and they end
// differently:
//
//   - Docker is unavailable and the instance is metadata-only. Nothing
//     contradicts the record, so the new password is simply stored — and a
//     container built later is seeded from it.
//   - The instance is not available (stopped, starting, failed). The container
//     keeps the credentials it was initialised with, and there is no later
//     moment at which Overcast could apply a pending one, so the modification
//     is refused rather than deferred.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
)

// masterPasswordTimeout bounds the password statement. It is one round trip to
// a local engine that is already accepting connections, so a command still
// running after this is stuck rather than slow.
const masterPasswordTimeout = 30 * time.Second

// engineClients maps an engine to the client binary its image ships. The
// Aurora variants run the open-source images, so they use the same clients.
var engineClients = map[string]string{
	"mysql":             "mysql",
	"aurora-mysql":      "mysql",
	"mariadb":           "mariadb",
	"postgres":          "psql",
	"aurora-postgresql": "psql",
}

// changeMasterPassword makes a new master password true of the instance, or
// says why it cannot be. It returns nil only when the caller may record the
// new password: either the engine has taken it, or there is no engine.
func (h *Handler) changeMasterPassword(ctx context.Context, inst *DBInstance, newPassword string) *protocol.AWSError {
	if !h.dockerReady.Load() || inst.DockerContainerID == "" {
		// Metadata-only: nothing is serving connections, so nothing can
		// disagree with the record. A container built for this instance later
		// is seeded from the record and starts life on the new password.
		return nil
	}
	if inst.DBInstanceStatus != "available" {
		return errInvalidDBInstanceState(inst.DBInstanceIdentifier,
			"the master password can only be changed while the instance is available — "+
				"the engine has to be running to accept it, and a stopped container keeps the credentials it was created with")
	}
	return h.applyMasterPassword(ctx, inst, newPassword)
}

// applyMasterPassword changes the master password on the engine running in the
// instance's container. It reports an error unless the engine accepted it.
func (h *Handler) applyMasterPassword(ctx context.Context, inst *DBInstance, newPassword string) *protocol.AWSError {
	cmd, env, ok := masterPasswordCommand(inst, newPassword)
	if !ok {
		return errMasterPasswordNotApplied(inst.DBInstanceIdentifier,
			"engine "+inst.Engine+" has no password-change path in Overcast")
	}

	execCtx, cancel := context.WithTimeout(ctx, masterPasswordTimeout)
	defer cancel()

	res, err := h.docker.Exec(execCtx, inst.DockerContainerID, cmd, env)
	if err != nil {
		return errMasterPasswordNotApplied(inst.DBInstanceIdentifier, err.Error())
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Output)
		if detail == "" {
			detail = "the engine's client exited " + strconv.Itoa(res.ExitCode) + " without saying why"
		}
		return errMasterPasswordNotApplied(inst.DBInstanceIdentifier, detail)
	}
	return nil
}

// masterPasswordCommand builds the command that sets a new master password on
// a running engine, and the environment it needs. It authenticates with the
// password the engine currently has, which is the one on the record.
func masterPasswordCommand(inst *DBInstance, newPassword string) (cmd, env []string, ok bool) {
	client, known := engineClients[inst.Engine]
	if !known {
		return nil, nil, false
	}

	switch client {
	case "psql":
		// The master user is the image's POSTGRES_USER and so the cluster
		// superuser; `postgres` is the database every image always has.
		// ON_ERROR_STOP makes a rejected statement a non-zero exit rather than
		// a message on a successful run.
		return []string{
				client,
				"--no-psqlrc",
				"-v", "ON_ERROR_STOP=1",
				"-U", inst.MasterUsername,
				"-d", "postgres",
				"-c", postgresPasswordStatement(inst.MasterUsername, newPassword),
			},
			[]string{"PGPASSWORD=" + inst.MasterUserPassword}, true

	default: // mysql, mariadb
		// Always as root: a non-root master user is created with rights on its
		// own database only, and cannot alter accounts. The container's root
		// password is the master password the instance was created with.
		return []string{
			client,
			"--protocol=socket",
			"-u", "root",
			"-p" + inst.MasterUserPassword,
			"-e", mysqlPasswordStatements(inst.MasterUsername, newPassword),
		}, nil, true
	}
}

// mysqlPasswordStatements sets the new password on every account the container
// was seeded with. `root` is included even when it is not the master user:
// startDBContainer passes the stored password as MYSQL_ROOT_PASSWORD, so
// leaving root behind would make the record wrong about a container rebuilt
// later. IF EXISTS covers the host forms an image did not create.
func mysqlPasswordStatements(masterUser, newPassword string) string {
	users := []string{"root"}
	if masterUser != "" && masterUser != "root" {
		users = append(users, masterUser)
	}

	var b strings.Builder
	for _, user := range users {
		for _, host := range []string{"localhost", "%"} {
			b.WriteString("ALTER USER IF EXISTS ")
			b.WriteString(mysqlString(user))
			b.WriteByte('@')
			b.WriteString(mysqlString(host))
			b.WriteString(" IDENTIFIED BY ")
			b.WriteString(mysqlString(newPassword))
			b.WriteString("; ")
		}
	}
	return strings.TrimSpace(b.String())
}

// postgresPasswordStatement sets the new password on the master role.
func postgresPasswordStatement(masterUser, newPassword string) string {
	return "ALTER USER " + pgIdentifier(masterUser) + " WITH PASSWORD " + pgString(newPassword) + ";"
}

// mysqlEscaper escapes the two characters that end a MySQL string literal
// early. MySQL treats a backslash as an escape character by default, so both
// it and the quote have to be escaped for the engine to receive the value as
// written.
var mysqlEscaper = strings.NewReplacer(`\`, `\\`, `'`, `''`)

// mysqlString renders a value as a MySQL string literal.
func mysqlString(s string) string {
	return "'" + mysqlEscaper.Replace(s) + "'"
}

// pgString renders a value as a PostgreSQL string literal. With
// standard_conforming_strings on — the default since 9.1 — a backslash is an
// ordinary character and only the quote needs doubling.
func pgString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// pgIdentifier renders a value as a quoted PostgreSQL identifier, which is
// also what preserves the case of a mixed-case role name.
func pgIdentifier(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
