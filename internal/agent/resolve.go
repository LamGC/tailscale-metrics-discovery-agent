package agent

import (
	"net"
	"net/netip"
	"strings"
)

// resolveContext holds the values available for variable substitution in
// service targets. Built once per handleServices request.
type resolveContext struct {
	tsIPv4   string // Tailscale IPv4 (e.g. "100.64.0.1")
	tsIPv6   string // Tailscale IPv6 (e.g. "fd7a:115c:a1e0::1")
	selfAddr string // {self} replacement for bucket/proxy (IP:port, based on requester)
}

// resolveTarget replaces variables in a target string.
// Returns the resolved string and true, or ("", false) if any variable
// cannot be resolved (meaning this target should be excluded).
//
// Supported variables:
//   - {ts.ip}                  → Tailscale IPv4
//   - {ts.ipv6}                → Tailscale IPv6
//   - {if.link.ip:<iface>}     → IPv4 of network interface <iface>
//   - {if.link.ipv6:<iface>}   → IPv6 of network interface <iface>
//   - {self}                   → internal: IP:port based on requester's address family
func resolveTarget(target string, rc *resolveContext) (string, bool) {
	// Fast path: no variables.
	if !strings.Contains(target, "{") {
		return target, true
	}

	var out strings.Builder
	out.Grow(len(target))

	for i := 0; i < len(target); {
		open := strings.IndexByte(target[i:], '{')
		if open < 0 {
			out.WriteString(target[i:])
			break
		}
		out.WriteString(target[i : i+open])
		closeLoc := strings.IndexByte(target[i+open:], '}')
		if closeLoc < 0 {
			// Unclosed brace — treat as literal.
			out.WriteString(target[i+open:])
			break
		}
		varName := target[i+open+1 : i+open+closeLoc]
		val, ok := resolveVar(varName, rc)
		if !ok {
			return "", false
		}
		out.WriteString(val)
		i = i + open + closeLoc + 1
	}
	return out.String(), true
}

// resolveVar resolves a single variable name (without braces).
func resolveVar(name string, rc *resolveContext) (string, bool) {
	switch name {
	case "ts.ip":
		if rc.tsIPv4 == "" {
			return "", false
		}
		return rc.tsIPv4, true
	case "ts.ipv6":
		if rc.tsIPv6 == "" {
			return "", false
		}
		return rc.tsIPv6, true
	case "self":
		if rc.selfAddr == "" {
			return "", false
		}
		return rc.selfAddr, true
	default:
		// {if.link.ip:<iface>} or {if.link.ipv6:<iface>}
		if strings.HasPrefix(name, "if.link.ip:") {
			iface := name[len("if.link.ip:"):]
			ip, err := interfaceIP(iface, false)
			if err != nil {
				return "", false
			}
			return ip, true
		}
		if strings.HasPrefix(name, "if.link.ipv6:") {
			iface := name[len("if.link.ipv6:"):]
			ip, err := interfaceIP(iface, true)
			if err != nil {
				return "", false
			}
			return ip, true
		}
		// Unknown variable.
		return "", false
	}
}

// interfaceIP returns the first IP address of the given network interface,
// filtered by address family.
func interfaceIP(ifaceName string, wantV6 bool) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if wantV6 && addr.Is6() && !addr.IsLinkLocalUnicast() {
			return addr.String(), nil
		}
		if !wantV6 && addr.Is4() {
			return addr.String(), nil
		}
	}
	return "", net.UnknownNetworkError("no matching address on " + ifaceName)
}

// selfAddrForRequest determines the appropriate {self} address (IP:port)
// based on the requester's RemoteAddr. If the requester connects via IPv6,
// returns [ipv6]:port; otherwise returns ipv4:port.
func selfAddrForRequest(remoteAddr, tsIPv4, tsIPv6, listenPort string) string {
	host, _, _ := net.SplitHostPort(remoteAddr)
	if host == "" {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)

	// If the requester is using IPv6, prefer our IPv6 address.
	if err == nil && addr.Is6() && tsIPv6 != "" {
		return "[" + tsIPv6 + "]:" + listenPort
	}
	// Default: IPv4.
	if tsIPv4 != "" {
		return tsIPv4 + ":" + listenPort
	}
	if tsIPv6 != "" {
		return "[" + tsIPv6 + "]:" + listenPort
	}
	return ""
}
