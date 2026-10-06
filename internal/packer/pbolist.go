package packer

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
)

// pboVersionMime marks the header entry that carries the PBO's properties
// (prefix, product, ...) rather than a file. It is "sreV" read little-endian.
const pboVersionMime = 0x56657273

// pboEntries lists the file names in a PBO's header.
//
// AddonBuilder's -packonly path copies the source with a CopyToTemp task that
// logs nothing per file, so its log cannot say what went in. The header can, and
// reading it checks the PBO that will actually ship rather than a log about it.
func pboEntries(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("packer: reading %s: %w", path, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)

	var names []string
	for {
		name, err := r.ReadString(0)
		if err != nil {
			return nil, fmt.Errorf("packer: %s: truncated PBO header: %w", path, err)
		}
		name = name[:len(name)-1]

		// mime, original size, reserved, timestamp, data size
		var fields [5]uint32
		if err := binary.Read(r, binary.LittleEndian, &fields); err != nil {
			return nil, fmt.Errorf("packer: %s: truncated PBO header: %w", path, err)
		}

		if name == "" && fields[0] == pboVersionMime {
			// Key/value property pairs, ended by an empty key. A value may be
			// empty, so only the key position can end the list.
			for {
				key, err := r.ReadString(0)
				if err != nil {
					return nil, fmt.Errorf("packer: %s: truncated PBO properties: %w", path, err)
				}
				if key == "\x00" {
					break
				}
				if _, err := r.ReadString(0); err != nil {
					return nil, fmt.Errorf("packer: %s: truncated PBO properties: %w", path, err)
				}
			}
			continue
		}
		if name == "" {
			// The empty entry that closes the header.
			return names, nil
		}
		names = append(names, name)
	}
}
