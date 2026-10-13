package node

import (
	"errors"
	"fmt"
	"os"

	jnode "github.com/amber-store/jaccard-store/node"
	"github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/wire"
)

// Key is the secret key of an endpoint. The zero value is no key: Listen
// and Dial refuse it.
type Key struct {
	secret key.SecretKey
}

// NewKey makes a new key, as a zathras does every time it starts.
func NewKey() (Key, error) {
	secret, err := key.GenerateSecretKey()
	if err != nil {
		return Key{}, fmt.Errorf("new key: %w", err)
	}
	return Key{secret: secret}, nil
}

// LoadOrCreateKey reads the key file at path, which holds the seed as 64
// hexadecimal characters, and creates it with mode 0600, and its directory,
// when it is missing.
//
// It is the key file of jaccard-store, read and written by its code: white
// space around the seed is allowed, and a file of any other shape is an
// error that names the path, repeats nothing of the contents and leaves
// the file as it is.
func LoadOrCreateKey(path string) (Key, error) {
	secret, err := jnode.LoadOrCreateKey(path)
	if err != nil {
		return Key{}, err
	}
	return Key{secret: secret}, nil
}

// LoadKey reads the key file at path, as LoadOrCreateKey does, and makes
// none: a file that is not there is an error. It is for a key that
// somebody was to put there, as the role key of a zathras is: a new one
// would be a key nobody granted anything.
func LoadKey(path string) (Key, error) {
	if _, err := os.Stat(path); err != nil {
		return Key{}, fmt.Errorf("the key file %s: %w", path, errors.Unwrap(err))
	}
	return LoadOrCreateKey(path)
}

// Sign returns the signature of the key over text: 64 bytes, of Ed25519,
// as every key of iroh signs.
func (k Key) Sign(text []byte) []byte {
	sig := k.secret.Sign(text).Bytes()
	return sig[:]
}

// Verify returns nil when signature is that of the key whose endpoint ID
// is id, over text.
func Verify(id wire.ID, text, signature []byte) error {
	public, err := key.ParseEndpointID(string(id))
	if err != nil {
		return fmt.Errorf("%q is no endpoint ID: %w", string(id), err)
	}
	sig, err := key.SignatureFromSlice(signature)
	if err != nil {
		return fmt.Errorf("a signature has %d bytes, and this one %d", key.SignatureSize, len(signature))
	}
	if err := public.PublicKey().Verify(text, sig); err != nil {
		return errors.New("the signature is not that of the key over this text")
	}
	return nil
}

// ID returns the endpoint ID of the key: its public half.
func (k Key) ID() wire.ID {
	return idOf(k.secret.Public().EndpointID())
}

// Iroh returns the key as go-iroh has it (key.SecretKey of
// github.com/tmc/go-iroh/key), for the packages that dial with a library
// of their own.
func (k Key) Iroh() key.SecretKey {
	return k.secret
}

// ParseID accepts an endpoint ID as 64 hexadecimal characters or as
// go-iroh writes it, and refuses what is no endpoint ID.
//
// go-iroh writes an ID as 64 lowercase hexadecimal characters, and reads
// the 52 characters of base32 of other iroh programs as well. Hexadecimal
// in capitals is accepted. What is returned is always in lower case.
func ParseID(text string) (wire.ID, error) {
	id, err := key.ParseEndpointID(text)
	if err != nil {
		return "", fmt.Errorf("%q is no endpoint ID: %w", text, err)
	}
	return idOf(id), nil
}

// idOf returns id as it travels: 64 lowercase hexadecimal characters.
func idOf(id key.EndpointID) wire.ID {
	return wire.ID(id.String())
}
