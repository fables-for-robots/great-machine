package closure

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// machO are the four bytes a Mach-O file begins with: the 32-bit and the
// 64-bit format in both byte orders, and the universal ("fat") file in
// both.
var machO = [][4]byte{
	{0xfe, 0xed, 0xfa, 0xce},
	{0xfe, 0xed, 0xfa, 0xcf},
	{0xce, 0xfa, 0xed, 0xfe},
	{0xcf, 0xfa, 0xed, 0xfe},
	{0xca, 0xfe, 0xba, 0xbe},
	{0xbe, 0xba, 0xfe, 0xca},
}

// CheckLinux refuses a Mach-O binary: the pods run Linux, and a binary
// built on a Mac for the Mac would be pushed and rolled out only to fail
// at its start. Anything else passes, a script as well as an ELF binary.
func CheckLinux(binary string) error {
	f, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			// Shorter than a magic number: no Mach-O binary.
			return nil
		}
		return fmt.Errorf("reading %s: %w", binary, err)
	}
	if slices.Contains(machO, magic) {
		return fmt.Errorf("%s is a macOS binary (Mach-O), and the pods run Linux: build it for Linux", binary)
	}
	return nil
}
