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

// ObservedList maps an observed attribute that is derived text to the
// list attribute that it shows, for the copy rule (review K3 of #248): the
// firewall's "ssh" ("tcp/<port> from <sources>") gives the elements of
// ssh_sources, so a hand-made SSH source matches a later copy into
// ssh_sources. ok is false for every other attribute. A closed SSH rule or
// "from everywhere" gives the empty list.
func ObservedList(typ, attr, v string) (list string, ok bool) {
	if typ != "firewall" || attr != fwAttrSSH {
		return "", false
	}
	_, src, isRule := fwSSHRule(v)
	if !isRule {
		return "", true
	}
	return NormalizeList(typ, "ssh_sources", src), true
}
