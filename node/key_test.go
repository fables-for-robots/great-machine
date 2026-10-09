package node_test

import (
	"encoding/base32"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewKey(t *testing.T) {
	a, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	b, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if !hexID.MatchString(string(a.ID())) {
		t.Errorf("ID %q is not 64 lowercase hexadecimal characters", a.ID())
	}
	if a.ID() == b.ID() {
		t.Errorf("two new keys have the same ID %s", a.ID())
	}
	if got := wire.ID(a.Iroh().Public().EndpointID().String()); got != a.ID() {
		t.Errorf("the key go-iroh gets has the ID %s, want %s", got, a.ID())
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "made", "on", "the", "way")
	path := filepath.Join(dir, "operator.key")

	created, err := node.LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey of a missing file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the key file was not created: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the key file has mode %o, want 600", got)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}\n$`).Match(content) {
		t.Errorf("the key file is not 64 hexadecimal characters and a line feed (it has %d bytes)", len(content))
	}

	again, err := node.LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey of the file it created: %v", err)
	}
	if again.ID() != created.ID() {
		t.Errorf("read again, the ID is %s; it was %s", again.ID(), created.ID())
	}
	if after, _ := os.ReadFile(path); string(after) != string(content) {
		t.Error("reading the key file changed it")
	}
}

func TestLoadOrCreateKeyReadsAKnownSeed(t *testing.T) {
	// The seed of RFC 8032, test 1, and its public key.
	const seed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	const id = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	for name, content := range map[string]string{
		"with a line feed":    seed + "\n",
		"without a line feed": seed,
		"in capitals":         strings.ToUpper(seed) + "\n",
		"with space around":   "  " + seed + " \r\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			k, err := node.LoadOrCreateKey(path)
			if err != nil {
				t.Fatalf("LoadOrCreateKey: %v", err)
			}
			if k.ID() != id {
				t.Errorf("got the ID %s, want %s", k.ID(), id)
			}
		})
	}
}

func TestLoadOrCreateKeyRefusesWhatIsNoKey(t *testing.T) {
	for name, content := range map[string]string{
		"empty":            "",
		"text":             "this is no key\n",
		"too short":        strings.Repeat("ab", 31) + "\n",
		"too long":         strings.Repeat("ab", 33) + "\n",
		"not hexadecimal":  strings.Repeat("zz", 32) + "\n",
		"a seed of zeros":  strings.Repeat("00", 32) + "\n",
		"two seeds":        strings.Repeat("ab", 32) + "\n" + strings.Repeat("cd", 32) + "\n",
		"very much of it":  strings.Repeat("ab", 4096),
		"an endpoint ID 0": "0\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := node.LoadOrCreateKey(path)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the error does not name the file: %v", err)
			}
			if after, _ := os.ReadFile(path); string(after) != content {
				t.Error("the file that is no key was changed")
			}
		})
	}
}

func TestParseID(t *testing.T) {
	k, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	id := string(k.ID())
	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	// Other iroh programs write an endpoint ID as base32 without padding.
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	valid := []struct {
		name string
		text string
	}{
		{"hexadecimal", id},
		{"hexadecimal in capitals", strings.ToUpper(id)},
		{"base32 in capitals", b32},
		{"base32 in lower case", strings.ToLower(b32)},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := node.ParseID(tt.text)
			if err != nil {
				t.Fatalf("ParseID(%q): %v", tt.text, err)
			}
			if string(got) != id {
				t.Errorf("ParseID(%q): got %s, want %s", tt.text, got, id)
			}
		})
	}

	invalid := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"a word", "draal"},
		{"63 characters", id[:63]},
		{"65 characters", id + "0"},
		{"64 characters that are no hexadecimal", strings.Repeat("zz", 32)},
		{"a line feed behind it", id + "\n"},
		{"a space before it", " " + id},
		{"the beginning of an ID", id[:8]},
		{"32 bytes that are no point of the curve", "02" + strings.Repeat("00", 31)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			got, err := node.ParseID(tt.text)
			if err == nil {
				t.Fatalf("ParseID(%q): got %s, want an error", tt.text, got)
			}
			if got != "" {
				t.Errorf("ParseID(%q): got %q beside the error", tt.text, got)
			}
		})
	}
}
