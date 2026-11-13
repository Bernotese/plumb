package resource

// NormalizeList returns a list-like string attribute (schema mark
// x-plumb-list, D-350) in the form that Diff compares, so the copy rule
// (D-331) hashes the elements of a desired value and of a reported value
// alike: firewall ports merged and sorted, networks masked and sorted. A
// value that does not parse, and every other attribute, comes back as it
// is.
func NormalizeList(typ, attr, v string) string {
	if typ != "firewall" {
		return v
	}
	var out string
	var err error
	switch attr {
	case "tcp_ports", "udp_ports":
		out, err = normPorts(v)
	case "sources", "ssh_sources":
		out, err = normSources(v)
	default:
		return v
	}
	if err != nil {
		return v
	}
	return out
}
