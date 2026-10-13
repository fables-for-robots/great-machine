package cli

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amber-store/jaccard-store/bucket"
	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// TestInvalidCommandLines holds every command to the exit status 2 and a
// message on stderr for a command line that is not valid. None of them
// gets as far as the network.
func TestInvalidCommandLines(t *testing.T) {
	// A command line that is not refused starts a draal in its data
	// directory: one of the test's, so that a refusal that is lost
	// leaves nothing in the tree.
	d := t.TempDir()
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string // in stderr
	}{
		// The operator commands.
		{"status: too many arguments", nil, []string{"status", "--draal", idA, "web", "api"}, `unexpected argument "api"`},
		{"status: a bad service name", nil, []string{"status", "--draal", idA, "Web"}, `"Web" is not the name of a service`},
		{"status: no --draal", nil, []string{"status"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"status: a bad --draal", nil, []string{"status", "--draal", "nonsense"}, `--draal: "nonsense"`},
		{"status: the variable stands in for --draal", map[string]string{"GREAT_MACHINE_DRAAL": "nonsense"},
			[]string{"status"}, `--draal: "nonsense"`},
		{"status: --draal wins over its variable", map[string]string{"GREAT_MACHINE_DRAAL": idA},
			[]string{"status", "--draal", "nonsense"}, `--draal: "nonsense"`},
		{"logs: no SERVICE", nil, []string{"logs", "--draal", idA}, "logs: SERVICE is missing; usage: great-machine logs SERVICE [ZATHRAS]"},
		{"logs: too many arguments", nil, []string{"logs", "--draal", idA, "web", "web-a", "web-b"}, `unexpected argument "web-b"`},
		{"logs: a flag behind SERVICE", nil, []string{"logs", "--draal", idA, "web", "-f"}, "logs: the flag -f has to come before SERVICE"},
		{"logs: no --draal", nil, []string{"logs", "-f", "web"}, "--draal is required"},
		{"shell: no SERVICE", nil, []string{"shell", "--draal", idA}, "shell: SERVICE is missing"},
		{"shell: a bad service name", nil, []string{"shell", "--draal", idA, "web_1"}, `"web_1" is not the name of a service`},
		{"shell: no --draal", nil, []string{"shell", "web"}, "--draal is required"},
		{"rollback: no SERVICE", nil, []string{"rollback", "--draal", idA}, "rollback: SERVICE is missing"},
		{"rollback: too many arguments", nil, []string{"rollback", "--draal", idA, "web", "api"}, `unexpected argument "api"`},
		{"rollback: a negative --parallel", nil, []string{"rollback", "--draal", idA, "--parallel", "-1", "web"}, "--parallel: -1 is not a number of zathrases"},
		{"rollback: a --parallel that is no number", nil, []string{"rollback", "--draal", idA, "--parallel", "many", "web"}, `"many"`},
		{"rollback: the variable stands in for --parallel", map[string]string{"GREAT_MACHINE_PARALLEL": "-3"},
			[]string{"rollback", "--draal", idA, "web"}, "--parallel: -3 is not a number of zathrases"},
		{"rollback: a variable that is no number", map[string]string{"GREAT_MACHINE_PARALLEL": "many"},
			[]string{"rollback", "--draal", idA, "web"}, `"many"`},
		{"rollback: --detach behind SERVICE", nil, []string{"rollback", "--draal", idA, "web", "--detach"}, "rollback: the flag --detach has to come before SERVICE"},
		{"rollback: no --draal", nil, []string{"rollback", "web"}, "--draal is required"},
		{"rollout: no SERVICE", nil, []string{"rollout", "--draal", idA}, "rollout: SERVICE is missing; usage: great-machine rollout SERVICE PATH [-- ARG...]"},
		{"rollout: no PATH", nil, []string{"rollout", "--draal", idA, "web"}, "rollout: PATH is missing"},
		{"rollout: no PATH before --", nil, []string{"rollout", "--draal", idA, "web", "--", "/nix/store/abc-web/bin/web"}, "rollout: PATH is missing"},
		{"rollout: an argument of the service without --", nil, []string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web", "--port", "80"},
			"rollout: the flag --port has to come before SERVICE, and the arguments of the service behind --"},
		{"rollout: --detach behind SERVICE", nil, []string{"rollout", "--draal", idA, "web", "--detach", "/nix/store/abc-web/bin/web"},
			"rollout: the flag --detach has to come before SERVICE"},
		{"rollout: --parallel behind SERVICE", nil, []string{"rollout", "--draal", idA, "web", "--parallel", "2", "/nix/store/abc-web/bin/web"},
			"rollout: the flag --parallel has to come before SERVICE"},
		{"rollout: a flag behind PATH and before --", nil, []string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web", "-v", "--", "x"},
			"rollout: the flag -v has to come before SERVICE"},
		{"rollout: a service name that begins like a flag", nil, []string{"rollout", "--draal", idA, "-web", "/nix/store/abc-web/bin/web"}, "flag provided but not defined: -web"},
		{"rollout: a service name with a dot", nil, []string{"rollout", "--draal", idA, "web.prod", "/nix/store/abc-web/bin/web"}, `"web.prod" is not the name of a service`},
		{"rollout: a negative --parallel", nil, []string{"rollout", "--draal", idA, "--parallel", "-1", "web", "/nix/store/abc-web/bin/web"}, "--parallel: -1"},
		{"rollout: --jobs 0", nil, []string{"rollout", "--draal", idA, "--jobs", "0", "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
		{"rollout: the variable stands in for --jobs", map[string]string{"GREAT_MACHINE_JOBS": "0"},
			[]string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
		// The store answers 64 requests of a connection at a time, and a
		// push has two under way when it goes from one step to the next.
		{"rollout: more --jobs than the store answers", nil, []string{"rollout", "--draal", idA, "--jobs", "33", "web", "/nix/store/abc-web/bin/web"},
			"--jobs: 33 is more than 32 store paths at a time, which is the most"},
		{"rollout: no --draal", nil, []string{"rollout", "web", "/nix/store/abc-web/bin/web"}, "--draal is required"},
		{"rollout: a bad --draal", nil, []string{"rollout", "--draal", "nonsense", "web", "/nix/store/abc-web/bin/web"}, `--draal: "nonsense"`},
		{"id: an argument", nil, []string{"id", "now"}, `id takes no argument, got "now"`},

		// draal.
		{"draal: no --data", nil, []string{"draal", "--s3-bucket", "packs"}, "--data is required: give it, or set GREAT_MACHINE_DATA"},
		{"draal: no --s3-bucket", nil, []string{"draal", "--data", d}, "--s3-bucket is required: give it, or set GREAT_MACHINE_S3_BUCKET"},
		// The flags of a draal of before, which was told where the store
		// is: it is the store now.
		{"draal: --jaccard-server, which is gone", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--jaccard-server", idB},
			"flag provided but not defined: -jaccard-server"},
		{"draal: --jaccard-prefix, which is gone", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--jaccard-prefix", "nix/"},
			"flag provided but not defined: -jaccard-prefix"},
		{"draal: an --s3-endpoint that is no URL", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--s3-endpoint", "minio:9000"},
			`--s3-endpoint: "minio:9000" is not the URL of an S3 service`},
		{"draal: the variable stands in for --s3-endpoint", map[string]string{"GREAT_MACHINE_S3_ENDPOINT": "s3.example.com"},
			[]string{"draal", "--data", d, "--s3-bucket", "packs"}, `--s3-endpoint: "s3.example.com" is not the URL of an S3 service`},
		// The endpoint is in every URL a client is handed: it names a
		// host, and carries nothing that is not the service's address.
		{"draal: an --s3-endpoint without a host", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--s3-endpoint", "http://:9000"},
			`--s3-endpoint: "http://:9000" is not the URL of an S3 service`},
		{"draal: an --s3-endpoint with a user in it", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--s3-endpoint", "http://user:secret@host"},
			`--s3-endpoint: a value with an @, a ? or a # in it, which is not repeated here, is not the URL of an S3 service`},
		{"draal: an --s3-endpoint with a query", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--s3-endpoint", "http://host/path?x=1"},
			`--s3-endpoint: a value with an @, a ? or a # in it, which is not repeated here, is not the URL of an S3 service`},
		{"draal: an --s3-endpoint with a fragment", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--s3-endpoint", "http://host#frag"},
			`--s3-endpoint: a value with an @, a ? or a # in it, which is not repeated here, is not the URL of an S3 service`},
		{"draal: a --store-max-pack-size that is no size", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--store-max-pack-size", "lots"},
			`--store-max-pack-size: "lots" is not a size: want bytes, or a number with KiB, MiB or GiB`},
		{"draal: a --store-max-pack-size of nothing", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--store-max-pack-size", "0"},
			`--store-max-pack-size: "0" is not a size`},
		{"draal: a --store-max-pack-size in a unit there is not", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--store-max-pack-size", "16GB"},
			`--store-max-pack-size: "16GB" is not a size`},
		{"draal: the variable stands in for --store-max-pack-size", map[string]string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE": "-1"},
			[]string{"draal", "--data", d, "--s3-bucket", "packs"}, `--store-max-pack-size: "-1" is not a size`},
		{"draal: a --store-max-pack-size with a sign", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--store-max-pack-size", "+1GiB"},
			`--store-max-pack-size: "+1GiB" is not a size`},
		{"draal: no --store-verify-jobs at all", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--store-verify-jobs", "0"},
			"--store-verify-jobs: 0 is not a number of packs to verify at once: want 1 or more"},
		{"draal: an operator that is no endpoint ID", nil,
			[]string{"draal", "--data", d, "--s3-bucket", "packs", "--operator", idA, "--operator", "alice"},
			`--operator: "alice" is not an endpoint ID`},
		{"draal: the variable stands in for --operator", map[string]string{"GREAT_MACHINE_OPERATORS": idA + ",bob"},
			[]string{"draal", "--data", d, "--s3-bucket", "packs"}, `--operator: "bob" is not an endpoint ID`},
		{"draal: a bad --bind", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "--bind", "4433"}, `--bind: "4433" is not an address to bind`},
		{"draal: the flag of the web page, which draal serves no more", nil,
			[]string{"draal", "--data", d, "--s3-bucket", "packs", "--web-addr", "127.0.0.1:8080"}, "flag provided but not defined: -web-addr"},
		{"draal: an argument", nil, []string{"draal", "--data", d, "--s3-bucket", "packs", "now"}, `draal takes no argument, got "now"`},

		// web.
		{"web: no --draal", nil, []string{"web", "--key", d + "/operator.key"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"web: a bad --draal", nil, []string{"web", "--draal", "nonsense", "--key", d + "/operator.key"}, `--draal: "nonsense"`},
		{"web: an --addr that is no address", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", "8080"},
			`--addr: "8080" is not an address to listen on: want HOST:PORT, such as 127.0.0.1:8080`},
		{"web: no --addr at all", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", ""},
			`--addr: "" is not an address to listen on`},
		// The page acts with the operator's key: it listens on loopback
		// alone.
		{"web: every address", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", ":8080"},
			`--addr: ":8080" is not a loopback address: the page acts with the operator's key, and listens on this machine alone`},
		{"web: every address of IPv4", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", "0.0.0.0:8080"},
			`--addr: "0.0.0.0:8080" is not a loopback address`},
		{"web: an address of the network", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", "10.1.2.3:8080"},
			`--addr: "10.1.2.3:8080" is not a loopback address`},
		{"web: a name that is not this machine's", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "--addr", "draal.example.com:8080"},
			`--addr: "draal.example.com:8080" is not a loopback address`},
		{"web: the variable stands in for --addr", map[string]string{"GREAT_MACHINE_WEB_ADDR": "[::]:8080"},
			[]string{"web", "--draal", idA, "--key", d + "/operator.key"}, `--addr: "[::]:8080" is not a loopback address`},
		{"web: an argument", nil, []string{"web", "--draal", idA, "--key", d + "/operator.key", "now"}, `web takes no argument, got "now"`},

		// zathras.
		{"zathras: no --draal", nil, []string{"zathras", "--service", "web"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"zathras: a bad --draal", nil, []string{"zathras", "--draal", "nonsense", "--service", "web"}, `--draal: "nonsense"`},
		{"zathras: no --service", nil, []string{"zathras", "--draal", idA}, "--service is required: give it, or set GREAT_MACHINE_SERVICE"},
		{"zathras: a bad --service", nil, []string{"zathras", "--draal", idA, "--service", "Web"}, `"Web" is not the name of a service`},
		{"zathras: the variable stands in for --service", map[string]string{"GREAT_MACHINE_SERVICE": "Web"},
			[]string{"zathras", "--draal", idA}, `"Web" is not the name of a service`},
		{"zathras: a --stop-timeout that is no duration", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "soon"}, `"soon"`},
		{"zathras: a --stop-timeout of nothing", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "0s"}, "--stop-timeout: 0s is not a time"},
		{"zathras: an argument", nil, []string{"zathras", "--draal", idA, "--service", "web", "now"}, `zathras takes no argument, got "now"`},
		{"zathras: a --user that is a name", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "nobody"}, `--user: "nobody" is no user: want UID or UID:GID, both numbers`},
		{"zathras: a --user below nothing", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "-1"}, `--user: "-1" is no user`},
		{"zathras: a --user above every ID", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "1000:4294967296"}, `--user: "1000:4294967296" is no user`},
		{"zathras: a --user with nothing behind the colon", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "1000:"}, `--user: "1000:" is no user`},
		{"zathras: a --user with nothing before the colon", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", ":100"}, `--user: ":100" is no user`},
		{"zathras: a store and no directory for its cache", nil, []string{"zathras", "--draal", idA, "--service", "web", "--store-cache", ""}, "--store-cache: the store needs a directory for its pack cache"},
		{"zathras: the variable stands in for --user", map[string]string{"GREAT_MACHINE_USER": "root"},
			[]string{"zathras", "--draal", idA, "--service", "web"}, `--user: "root" is no user`},
		{"zathras: the flag of the sidecar's endpoint, which is gone", nil,
			[]string{"zathras", "--draal", idA, "--service", "web", "--preload-url", "http://127.0.0.1:9889/v1/preload"}, "flag provided but not defined: -preload-url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanEnvironment(t)
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			var stdout, stderr bytes.Buffer
			got := run(context.Background(), newApp(&stdout, &stderr), append([]string{"great-machine"}, tt.args...))
			if got != 2 {
				t.Errorf("the exit status is %d, want 2", got)
			}
			if !strings.HasPrefix(stderr.String(), "great-machine: ") || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr is %q, want it to say %q", stderr.String(), tt.want)
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Errorf("stderr is more than one line: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is %q, want nothing", stdout.String())
			}
		})
	}
}

func TestReadDraal(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readDraal(c)
		return err
	}

	// The least there is to say: where the data is, and the bucket. The
	// store has its defaults.
	read(t, draalCommand(), reader, "--data", "data", "--s3-bucket", "packs")
	want := draalConfig{
		data:  "data",
		store: storeConfig{bucket: bucket.Config{Bucket: "packs"}, verifyJobs: 2, maxPackBytes: 16 << 30},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the least: got %+v, want %+v", got, want)
	}

	// The variables alone. The operators have commas between them, and
	// what a comma too many leaves empty is passed over.
	t.Setenv("GREAT_MACHINE_DATA", "/var/lib/draal")
	t.Setenv("GREAT_MACHINE_OPERATORS", idA+", "+idB+",")
	t.Setenv("GREAT_MACHINE_S3_BUCKET", "packs")
	t.Setenv("GREAT_MACHINE_S3_PREFIX", "great-machine")
	t.Setenv("GREAT_MACHINE_S3_ENDPOINT", "http://rustfs.storage:9000")
	t.Setenv("GREAT_MACHINE_S3_REGION", "eu-central-1")
	t.Setenv("GREAT_MACHINE_S3_PATH_STYLE", "true")
	t.Setenv("GREAT_MACHINE_STORE_SCRATCH", "/scratch")
	t.Setenv("GREAT_MACHINE_STORE_VERIFY_JOBS", "4")
	t.Setenv("GREAT_MACHINE_STORE_MAX_PACK_SIZE", "64MiB")
	t.Setenv("GREAT_MACHINE_STORE_NO_VERIFY", "true")
	read(t, draalCommand(), reader)
	want = draalConfig{
		data:   "/var/lib/draal",
		admins: []wire.ID{wire.ID(idA), wire.ID(idB)},
		store: storeConfig{
			bucket:       bucket.Config{Bucket: "packs", Prefix: "great-machine", Endpoint: "http://rustfs.storage:9000", Region: "eu-central-1", PathStyle: true},
			scratch:      "/scratch",
			verifyJobs:   4,
			maxPackBytes: 64 << 20,
			noVerify:     true,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the variables: got %+v, want %+v", got, want)
	}

	// A scratch directory of blanks is none: the store then makes its own
	// in the data directory.
	read(t, draalCommand(), reader, "--data", "data", "--store-scratch", "   ")
	if got.store.scratch != "" {
		t.Errorf("--store-scratch of blanks is read as %q, want none", got.store.scratch)
	}
	// And an endpoint may have a path, which some services are under.
	read(t, draalCommand(), reader, "--data", "data", "--s3-endpoint", "https://gateway.example.com/s3")
	if got.store.bucket.Endpoint != "https://gateway.example.com/s3" {
		t.Errorf("--s3-endpoint with a path is read as %q", got.store.bucket.Endpoint)
	}

	// A flag wins over its variable, and --operator may be repeated.
	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA, "--bind", "0.0.0.0:4433",
		"--s3-bucket", "other", "--s3-prefix", "", "--s3-endpoint", "https://s3.example.com", "--s3-region", "auto", "--s3-path-style=false",
		"--store-scratch", "scratch", "--store-verify-jobs", "1", "--store-max-pack-size", "1073741824", "--store-no-verify=false")
	want = draalConfig{
		data:   "data",
		admins: []wire.ID{wire.ID(idC), wire.ID(idA)},
		store: storeConfig{
			bucket:       bucket.Config{Bucket: "other", Endpoint: "https://s3.example.com", Region: "auto"},
			scratch:      "scratch",
			verifyJobs:   1,
			maxPackBytes: 1 << 30,
		},
		bind: netip.MustParseAddrPort("0.0.0.0:4433"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the flags: got %+v, want %+v", got, want)
	}
}

// A size is bytes, or a number with a unit of 1024.
func TestParseSize(t *testing.T) {
	for text, want := range map[string]int64{
		"1":       1,
		"4096":    4096,
		"5KiB":    5 << 10,
		"64MiB":   64 << 20,
		"16GiB":   16 << 30,
		" 2 GiB ": 2 << 30,
	} {
		if got, err := parseSize(text); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "0", "-1", "+1", "+1GiB", "0x10", "1e3", "lots", "16GB", "1.5GiB", "GiB", "9223372036854775807GiB", "1TiB"} {
		if got, err := parseSize(text); err == nil {
			t.Errorf("parseSize(%q) = %d, want an error", text, got)
		}
	}
}

// TestReadWeb: --addr has a default, its variable stands in for it, the
// flag wins, and every loopback address is taken; --draal and --key are
// those of every operator command.
func TestReadWeb(t *testing.T) {
	cleanEnvironment(t)
	var got webConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readWeb(c)
		return err
	}
	args := []string{"--draal", idA, "--key", "operator.key"}
	with := func(more ...string) []string { return append(append([]string{}, args...), more...) }

	read(t, webCommand(), reader, args...)
	if got.addr != "127.0.0.1:8080" || got.operator.draal != wire.ID(idA) || got.operator.key != "operator.key" {
		t.Errorf("the defaults: got %+v, want 127.0.0.1:8080 and the draal and the key that were given", got)
	}
	for _, addr := range []string{"localhost:9000", "127.0.0.1:0", "[::1]:9000", "127.9.9.9:80"} {
		read(t, webCommand(), reader, with("--addr", addr)...)
		if got.addr != addr {
			t.Errorf("--addr %s: got %q", addr, got.addr)
		}
	}
	read(t, webCommand(), reader, with("--addr", " [::1]:9000 ")...)
	if got.addr != "[::1]:9000" {
		t.Errorf("an address with blanks around it: got %q, want [::1]:9000", got.addr)
	}

	t.Setenv("GREAT_MACHINE_WEB_ADDR", "localhost:8081")
	read(t, webCommand(), reader, args...)
	if got.addr != "localhost:8081" {
		t.Errorf("the variable: got %q, want localhost:8081", got.addr)
	}
	read(t, webCommand(), reader, with("--addr", "127.0.0.1:8082")...)
	if got.addr != "127.0.0.1:8082" {
		t.Errorf("the flag over the variable: got %q, want 127.0.0.1:8082", got.addr)
	}
}

func TestReadZathras(t *testing.T) {
	cleanEnvironment(t)
	var got zathrasConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readZathras(c)
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}

	// The defaults of the design.
	read(t, zathrasCommand(), reader, "--draal", idA, "--service", "web", "--role-key", "/keys/web.key")
	want := zathrasConfig{
		draal:       wire.ID(idA),
		service:     "web",
		name:        host,
		shell:       "/bin/sh",
		stopTimeout: 2 * time.Minute,
		healthAddr:  ":9890",
		storeCache:  "/var/cache/great-machine",
		roleKey:     "/keys/web.key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the defaults: got %+v, want %+v", got, want)
	}

	// The variables, and the flags that win over theirs. An empty
	// --health-addr is no health check.
	t.Setenv("GREAT_MACHINE_DRAAL", idB)
	t.Setenv("GREAT_MACHINE_SERVICE", "api")
	t.Setenv("GREAT_MACHINE_NAME", "api-0")
	t.Setenv("GREAT_MACHINE_SHELL", "/bin/bash")
	t.Setenv("GREAT_MACHINE_STOP_TIMEOUT", "30s")
	t.Setenv("GREAT_MACHINE_HEALTH_ADDR", "127.0.0.1:9999")
	t.Setenv("GREAT_MACHINE_STORE_CACHE", "/cache")
	t.Setenv("GREAT_MACHINE_NO_STORE", "true")
	t.Setenv("GREAT_MACHINE_USER", "1000:100")
	t.Setenv("GREAT_MACHINE_ROLE_KEY", "/run/secrets/api.key")
	read(t, zathrasCommand(), reader, "--name", "api-1", "--health-addr", "")
	want = zathrasConfig{
		draal:       wire.ID(idB),
		service:     "api",
		name:        "api-1",
		shell:       "/bin/bash",
		stopTimeout: 30 * time.Second,
		healthAddr:  "",
		storeCache:  "/cache",
		noStore:     true,
		user:        &launcher.User{UID: 1000, GID: 100},
		roleKey:     "/run/secrets/api.key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the variables: got %+v (user %+v), want %+v (user %+v)", got, got.user, want, want.user)
	}

	// A user without a group has the group of the same number.
	read(t, zathrasCommand(), reader, "--store-cache", "/var/cache/other", "--user", "65534", "--no-store=false")
	want.name, want.healthAddr = "api-0", "127.0.0.1:9999"
	want.storeCache, want.noStore, want.user = "/var/cache/other", false, &launcher.User{UID: 65534, GID: 65534}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the flags: got %+v (user %+v), want %+v (user %+v)", got, got.user, want, want.user)
	}
}

// A health check that cannot listen ends zathras at once, before it has
// made a key or dialed anyone. Without a store of its own, which it would
// have mounted before.
func TestZathrasHealthAddressTaken(t *testing.T) {
	cleanEnvironment(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), newApp(&stdout, &stderr),
		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--no-store", "--health-addr", taken.Addr().String(), "--role-key", aRoleKey(t)})
	if got != 1 || !strings.Contains(stderr.String(), "great-machine: the health check: listen tcp") {
		t.Errorf("the exit status is %d and stderr %q, want 1 and the failure to listen", got, stderr.String())
	}
}

func TestID(t *testing.T) {
	cleanEnvironment(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "operator.key")
	ids := map[string]bool{}
	for range 2 {
		var stdout, stderr bytes.Buffer
		if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id", "--key", path}); got != 0 {
			t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
		}
		id, ok := strings.CutSuffix(stdout.String(), "\n")
		if !ok || len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Fatalf("stdout is %q, want 64 hexadecimal characters and a line feed", stdout.String())
		}
		ids[id] = true
	}
	if len(ids) != 1 {
		t.Errorf("the ID changed between two runs: %v", ids)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the key file was not created: %v", err)
	}

	// The variable stands in for the flag, and the flag wins over it.
	other := filepath.Join(dir, "other.key")
	t.Setenv("GREAT_MACHINE_KEY", other)
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id"}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the key file the variable names was not created: %v", err)
	}
	if ids[strings.TrimSuffix(stdout.String(), "\n")] {
		t.Error("the variable was not read: the ID is that of the first key")
	}
	stdout.Reset()
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id", "--key", path}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if !ids[strings.TrimSuffix(stdout.String(), "\n")] {
		t.Error("the flag did not win over the variable")
	}
}

// The default place of the operator's key is in the user's configuration
// directory.
func TestIDDefaultKey(t *testing.T) {
	cleanEnvironment(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id"}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "great-machine", "operator.key")); err != nil {
		t.Errorf("the key file was not created in %s: %v", dir, err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("the configuration directory %s is outside the test's home", dir)
	}
}

// TestRunExitStatus: a command that returns a cli.ExitCoder chooses the
// exit status, and a message that is empty is not printed.
func TestRunExitStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		stderr string
	}{
		{"the status of the remote shell", cli.Exit("", 7), 7, ""},
		{"an interrupt", interrupted("interrupted: rollout #%d goes on", 13), 130, "great-machine: interrupted: rollout #13 goes on\n"},
		{"an interrupt with nothing to say", interrupted(""), 130, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newApp(&stdout, &stderr)
			app.Commands = append(app.Commands, &cli.Command{
				Name:   "fail",
				Action: func(*cli.Context) error { return tt.err },
			})
			if got := run(context.Background(), app, []string{"great-machine", "fail"}); got != tt.want {
				t.Errorf("run returned %d, want %d", got, tt.want)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
		})
	}
}

func TestHelpNamesEveryCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "--help"}); got != 0 {
		t.Fatalf("the exit status is %d", got)
	}
	for _, name := range []string{"draal", "zathras", "id", "rollout", "rollback", "status", "logs", "shell", "tui", "version"} {
		if !strings.Contains(stdout.String(), "\n   "+name+" ") {
			t.Errorf("the help does not name %s:\n%s", name, stdout.String())
		}
	}
}

// A refusal goes to the log of a pod. An endpoint that is refused and has
// an @, a ? or a # in it is not repeated there at all: a user, a query
// and a fragment are where somebody may have put a secret, and no cut of
// such a value is sure to leave it out.
func TestARefusedEndpointThatMayHaveASecretInItIsNotRepeated(t *testing.T) {
	for _, endpoint := range []string{
		"https://key:hush@s3.example.com",
		"https://s3.example.com/path?signature=hush",
		"https://s3.example.com/path#hush",
		"https://key:hu?sh@s3.example.com/path?signature=hush#hush",
		// An @ behind a ? or a #, and one before the scheme.
		"https://s3.example.com/path?signature=abc@hush",
		"https://s3.example.com/path#abc@hush",
		"https://s3.example.com?email=a@hush.example&k=hush",
		"hush@h://a@hush",
		// What is no URL at all.
		"https://key:hush@s3 .example.com",
		"key:hush@s3.example.com",
	} {
		for _, how := range []string{"flag", "variable"} {
			t.Run(how+" "+endpoint, func(t *testing.T) {
				cleanEnvironment(t)
				args := []string{"great-machine", "draal", "--data", t.TempDir(), "--s3-bucket", "packs"}
				if how == "flag" {
					args = append(args, "--s3-endpoint", endpoint)
				} else {
					t.Setenv("GREAT_MACHINE_S3_ENDPOINT", endpoint)
				}
				// A command line that is not refused would start a
				// draal: it gets a context that ends.
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				var stdout, stderr bytes.Buffer
				if got := run(ctx, newApp(&stdout, &stderr), args); got != 2 {
					t.Errorf("the exit status is %d, want 2", got)
				}
				if !strings.Contains(stderr.String(), "--s3-endpoint: ") || !strings.Contains(stderr.String(), "is not the URL of an S3 service") {
					t.Errorf("stderr is %q, want the refusal of the endpoint", stderr.String())
				}
				if strings.Contains(stderr.String(), "hush") || strings.Contains(stderr.String(), "sh@") || strings.Contains(stderr.String(), "s3.example.com") {
					t.Errorf("stderr repeats the endpoint, which may have a secret in it: %q", stderr.String())
				}
				if !strings.Contains(stderr.String(), "is not repeated here") {
					t.Errorf("stderr is %q, want it to say that the value is not repeated", stderr.String())
				}
			})
		}
	}
}
