package main

import (
	"net"
	"strings"
	"time"
)

// netbiosName does an NBNS node-status query (UDP 137) and returns the host's
// NetBIOS computer name (e.g. "IGOR-PC", "MATIC"). Pure Go, cross-platform — this
// is how Windows machines with no DNS/mDNS record get a name. Not SMB: NetBIOS name
// service is UDP 137 and answers even when TCP 139/445 are closed.
func netbiosName(ip string) string {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(ip, "137"), 700*time.Millisecond)
	if err != nil {
		return ""
	}
	defer conn.Close()

	// NBSTAT node-status query for the wildcard name "*": encoded as
	// 0x20 (len 32) + "CKAA…AA" + 0x00, then type NBSTAT (0x0021), class IN (0x0001).
	req := []byte{
		0x00, 0x00, // transaction id
		0x00, 0x00, // flags
		0x00, 0x01, // qdcount
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // an/ns/ar count
		0x20, // name length
	}
	req = append(req, []byte("CKAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")...)
	req = append(req, 0x00)       // name terminator
	req = append(req, 0x00, 0x21) // type NBSTAT
	req = append(req, 0x00, 0x01) // class IN

	_ = conn.SetDeadline(time.Now().Add(800 * time.Millisecond))
	if _, err := conn.Write(req); err != nil {
		return ""
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n < 12 {
		return ""
	}
	resp := buf[:n]

	// Parse the header counts — a node-status response usually has qdcount=0.
	qd := int(resp[4])<<8 | int(resp[5])
	an := int(resp[6])<<8 | int(resp[7])
	if an < 1 {
		return ""
	}
	off := 12
	for i := 0; i < qd; i++ { // skip any echoed questions
		off = skipNBName(resp, off)
		off += 4 // qtype + qclass
	}
	off = skipNBName(resp, off) // answer RR name
	off += 2 + 2 + 4 + 2        // type + class + ttl + rdlength
	if off >= n {
		return ""
	}
	numNames := int(resp[off])
	off++
	for i := 0; i < numNames; i++ {
		if off+18 > n {
			break
		}
		name := strings.TrimRight(string(resp[off:off+15]), " \x00")
		suffix := resp[off+15]
		group := resp[off+16]&0x80 != 0 // high bit of the flags word = group name
		off += 18
		// suffix 0x00 + unique = the Workstation / computer name.
		if suffix == 0x00 && !group && name != "" {
			return name
		}
	}
	return ""
}

// skipNBName advances past a (possibly compressed) NetBIOS name field.
func skipNBName(b []byte, off int) int {
	if off >= len(b) {
		return off
	}
	if b[off]&0xC0 == 0xC0 { // compression pointer
		return off + 2
	}
	for off < len(b) && b[off] != 0x00 {
		off += int(b[off]) + 1
	}
	return off + 1 // past the 0x00 terminator
}
