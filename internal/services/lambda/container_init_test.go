package lambda

// container_init_test.go — what a Lambda container is created as, now that
// Overcast's init is its entrypoint.
//
// Three things have to hold for every execution environment, zip or image: the
// provisioning archive carries the init at initproto.InitPath, the container's
// entrypoint is that path, and the argument list is exactly the command the
// container would have run without it. The last one is the delicate part —
// before this the Docker daemon merged an image's own ENTRYPOINT and CMD into
// a create request that left them empty, and it can no longer do that because
// the request now carries the init.

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/services/lambda/initbin"
	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// zipFunction is a zip-packaged function with real code, so the acquire gets
// as far as the create this file is about.
func zipFunction(t *testing.T) *Function {
	t.Helper()
	return &Function{
		Name:          "zip-fn",
		ARN:           "arn:aws:lambda:us-east-1:000000000000:function:zip-fn",
		Runtime:       "nodejs20.x",
		Handler:       "index.handler",
		PackageType:   "Zip",
		Architectures: []string{"x86_64"},
		MemorySize:    128,
		CodeZip:       testZip(t, map[string]string{"index.js": "exports.handler = async () => ({});"}),
	}
}

// acquireAgainstDaemon runs one cold start against the fake daemon, which
// refuses to start containers, and returns the create request it recorded.
func acquireAgainstDaemon(t *testing.T, daemon *recordingDaemon, fn *Function) docker.CreateContainerRequest {
	t.Helper()
	cr := newDaemonContainerRuntime(t, daemon.Server)
	if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure, got a running instance")
	}
	creates := daemon.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 container create, got %d", len(creates))
	}
	return creates[0]
}

// A zip function's container runs the init, with the base image's entrypoint
// and the handler after it — which is precisely what the shell bootstrap this
// replaces used to exec.
func TestAcquire_zipFunction_runsTheInitWithTheBaseImageEntrypoint(t *testing.T) {
	daemon := newRecordingDaemon(t)
	create := acquireAgainstDaemon(t, daemon, zipFunction(t))

	if got := create.ContainerConfig.Entrypoint; !slices.Equal(got, []string{initproto.InitPath}) {
		t.Errorf("entrypoint = %v, want [%s]", got, initproto.InitPath)
	}
	if got := create.ContainerConfig.Cmd; !slices.Equal(got, []string{"/lambda-entrypoint.sh", "index.handler"}) {
		t.Errorf("cmd = %v, want the base image entrypoint and the handler", got)
	}
}

// The runtime and the extensions are told the AWS value, because what they talk
// to is the init's own Runtime API inside the container. The host's
// per-environment endpoint is the init's business, and travels separately.
func TestAcquire_containerEnvNamesTheInitsRuntimeAPI(t *testing.T) {
	daemon := newRecordingDaemon(t)
	create := acquireAgainstDaemon(t, daemon, zipFunction(t))

	env := envMap(create.ContainerConfig.Env)
	if got := env["AWS_LAMBDA_RUNTIME_API"]; got != initproto.LambdaRuntimeAPI {
		t.Errorf("AWS_LAMBDA_RUNTIME_API = %q, want %q", got, initproto.LambdaRuntimeAPI)
	}
	host := env[initproto.EnvRuntimeAPI]
	if host == "" {
		t.Fatalf("%s is not set, so the init has no host endpoint to proxy to", initproto.EnvRuntimeAPI)
	}
	if host == initproto.LambdaRuntimeAPI {
		t.Errorf("%s = %q, want this environment's own host endpoint", initproto.EnvRuntimeAPI, host)
	}
	// The per-environment listener is what identifies the container, so the
	// address must carry a port of its own rather than the shared one.
	if _, port, ok := strings.Cut(host, ":"); !ok || port == "" || port == "0" {
		t.Errorf("%s = %q, want host:port", initproto.EnvRuntimeAPI, host)
	}
}

// An image function with no ImageConfig runs the image's own ENTRYPOINT+CMD,
// read back from the daemon because the daemon can no longer merge them in.
func TestAcquire_imageFunction_fallsBackToTheImagesOwnEntrypoint(t *testing.T) {
	daemon := newRecordingDaemon(t)
	create := acquireAgainstDaemon(t, daemon, imageFunction())

	if got := create.ContainerConfig.Entrypoint; !slices.Equal(got, []string{initproto.InitPath}) {
		t.Errorf("entrypoint = %v, want [%s]", got, initproto.InitPath)
	}
	if got := create.ContainerConfig.Cmd; !slices.Equal(got, []string{"/lambda-entrypoint.sh", "index.handler"}) {
		t.Errorf("cmd = %v, want the image's own ENTRYPOINT and CMD", got)
	}
}

// ImageConfig overrides follow Docker's own merge rule, so an image function
// runs exactly the process it ran before: an entrypoint override replaces the
// image's and drops the image's CMD with it, and a command override on its own
// replaces only the CMD.
func TestAcquire_imageFunction_imageConfigOverrides(t *testing.T) {
	for _, tc := range []struct {
		name        string
		imageConfig *ImageConfig
		wantCmd     []string
	}{
		{
			name:        "entrypoint override drops the image's CMD",
			imageConfig: &ImageConfig{EntryPoint: []string{"/usr/local/bin/custom"}},
			wantCmd:     []string{"/usr/local/bin/custom"},
		},
		{
			name:        "entrypoint and command override both",
			imageConfig: &ImageConfig{EntryPoint: []string{"/usr/local/bin/custom"}, Command: []string{"app.handler"}},
			wantCmd:     []string{"/usr/local/bin/custom", "app.handler"},
		},
		{
			name:        "command alone keeps the image's entrypoint",
			imageConfig: &ImageConfig{Command: []string{"other.handler"}},
			wantCmd:     []string{"/lambda-entrypoint.sh", "other.handler"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := newRecordingDaemon(t)
			fn := imageFunction()
			fn.ImageConfig = tc.imageConfig
			create := acquireAgainstDaemon(t, daemon, fn)

			if got := create.ContainerConfig.Cmd; !slices.Equal(got, tc.wantCmd) {
				t.Errorf("cmd = %v, want %v", got, tc.wantCmd)
			}
		})
	}
}

// WorkingDirectory stays a container-config field: it is Docker's job, and the
// init inherits whatever cwd it is given.
func TestAcquire_imageFunction_workingDirectoryStaysAContainerConfigField(t *testing.T) {
	daemon := newRecordingDaemon(t)
	fn := imageFunction()
	fn.ImageConfig = &ImageConfig{WorkingDirectory: "/srv"}
	create := acquireAgainstDaemon(t, daemon, fn)

	if create.ContainerConfig.WorkingDir != "/srv" {
		t.Errorf("working dir = %q, want /srv", create.ContainerConfig.WorkingDir)
	}
}

// A build with no init artefact for the function's architecture fails the cold
// start, loudly and before anything is created. A silent fallback would be an
// execution environment whose output cannot be attributed to an invocation,
// which is the failure this whole mechanism exists to remove.
func TestAcquire_withoutAnInitArtefact_failsTheColdStartBeforeCreating(t *testing.T) {
	if initbin.Present("s390x") {
		t.Fatal("the test relies on there being no init for an architecture Lambda does not offer")
	}
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	fn := zipFunction(t)
	fn.Architectures = []string{"s390x"}

	_, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false)
	if err == nil {
		t.Fatal("a cold start with no init for the architecture reported success")
	}
	if !strings.Contains(err.Error(), "s390x") {
		t.Errorf("the error does not name the architecture: %v", err)
	}
	if creates := daemon.recordedCreates(); len(creates) != 0 {
		t.Errorf("a container was created despite there being no init: %+v", creates)
	}
}

// Every container gets the init, zip or image — before this, image functions
// were not sent a provisioning archive at all.
func TestProvisioningArchive_carriesTheInitForEveryPackageType(t *testing.T) {
	for _, arch := range []string{"x86_64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			if err := appendLambdaInit(tw, []string{arch}); err != nil {
				t.Fatalf("appendLambdaInit: %v", err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}

			want := strings.TrimPrefix(initproto.InitPath, "/")
			mode, ok := tarEntryMode(t, buf.Bytes(), want)
			if !ok {
				t.Fatalf("the archive has no %s entry: %v", want, tarEntryNames(t, buf.Bytes()))
			}
			if mode != 0o755 {
				t.Errorf("%s mode = %o, want 755", want, mode)
			}
			if size := tarEntrySize(t, buf.Bytes(), want); size == 0 {
				t.Errorf("%s is empty", want)
			}
			// The directory is Overcast's own and exists in no base image, so
			// unlike /var and /opt it does get a header.
			dir, _, _ := strings.Cut(want, "/")
			if _, ok := tarEntryMode(t, buf.Bytes(), dir+"/"); !ok {
				t.Errorf("the archive does not create %s/", dir)
			}
		})
	}
}

// tarEntrySize returns the byte size of a named entry.
func tarEntrySize(t *testing.T, tarData []byte, name string) int64 {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		if hdr.Name == name {
			return hdr.Size
		}
	}
}
