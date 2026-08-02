//go:build windows

package netwait

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	afInet           = 2
	udpTableOwnerPID = 1
)

// mibUDPRowOwnerPID mirrors MIB_UDPROW_OWNER_PID.
type mibUDPRowOwnerPID struct {
	LocalAddr uint32
	LocalPort uint32 // network byte order in the low word
	OwningPID uint32
}

// portBound reports whether any process holds the UDP port.
//
// This reads the same table netstat does instead of trying to bind the port
// ourselves. A bind probe gets it wrong in both directions: binding 0.0.0.0
// succeeds while another process holds 127.0.0.1, and it would also miss a
// listener that set SO_REUSEADDR.
func portBound(port int) bool {
	ports, err := boundUDPPorts()
	if err != nil {
		// Fall back to the probe instead of reporting a wait that never ends.
		return !udpPortFree(port)
	}
	return ports[uint16(port)]
}

func boundUDPPorts() (map[uint16]bool, error) {
	var size uint32
	// The first call sizes the buffer, so ERROR_INSUFFICIENT_BUFFER is expected.
	_, _, _ = procGetExtendedUdpTable.Call(
		0,
		uintptr(unsafe.Pointer(&size)),
		0,
		afInet,
		udpTableOwnerPID,
		0,
	)
	if size == 0 {
		return nil, windows.ERROR_INVALID_PARAMETER
	}

	buf := make([]byte, size)
	ret, _, _ := procGetExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		afInet,
		udpTableOwnerPID,
		0,
	)
	if ret != 0 {
		return nil, windows.Errno(ret)
	}

	// The table is a DWORD count followed by that many rows.
	numEntries := binary.LittleEndian.Uint32(buf[:4])
	rowSize := int(unsafe.Sizeof(mibUDPRowOwnerPID{}))

	out := make(map[uint16]bool, numEntries)
	for i := 0; i < int(numEntries); i++ {
		off := 4 + i*rowSize
		if off+rowSize > len(buf) {
			break
		}
		row := (*mibUDPRowOwnerPID)(unsafe.Pointer(&buf[off]))
		// dwLocalPort holds the port in network byte order in its low word.
		out[ntohs(uint16(row.LocalPort))] = true
	}
	return out, nil
}

func ntohs(v uint16) uint16 { return v<<8 | v>>8 }
