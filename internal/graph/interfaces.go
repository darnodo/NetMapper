package graph

// addDevicePorts builds the ports a device described itself, from the `interfaces` fact family. The
// name is already canonical: the family marks it Canonical, so the parser applied the pack's naming
// rules at collection time, when the platform was known (FR-002, FR-003).
func (p *projection) addDevicePorts(rows []famRow) {
	for _, r := range rows {
		name := str(r.row, "name")
		if name == "" {
			continue
		}
		port := r.owner.port(name, sourceDevice, r.at)
		port.source = sourceDevice
		// A field the recipe did not collect is absent from the row, and absent stays absent: a
		// second observation must not blank what the first one found.
		if v := str(r.row, "description"); v != "" {
			port.description = v
		}
		if v := str(r.row, "admin_state"); v != "" {
			port.adminState = v
		}
		if v := str(r.row, "oper_state"); v != "" {
			port.operState = v
		}
		if v := str(r.row, "mac"); v != "" {
			port.mac = v
		}
		if v := num(r.row, "speed_bps"); v != nil {
			port.speedBPS = v
		}
		if v := num(r.row, "mtu"); v != nil {
			port.mtu = v
		}
		port.evidence[r.obs] = true
		port.see(name, sourceDevice, r.obs)
	}
}

// addReportedPorts creates the ports a neighbour report names that no `interfaces` row described, on
// either end, and records every spelling against the port it names. Creating the port rather than
// dropping the report is what keeps a cable whose interfaces recipe failed, and what gives the far
// end of a link something to attach to (FR-004, FR-006, edge case).
func (p *projection) addReportedPorts(reports []*report) {
	for _, r := range reports {
		// The port is `neighbour` because the interfaces recipe never listed it, which is the
		// distinction FR-006 asks for. The spelling is `device` because the device that owns the port
		// is the one that wrote it: a neighbours row names its own local interface. The two columns
		// answer two different questions, and only the second one is about vocabulary.
		local := r.from.port(r.fromPort, sourceNeighbour, r.at)
		local.evidence[r.obs] = true
		local.see(r.fromSpelling, sourceDevice, r.obs)

		if r.to == nil || r.toPort == "" {
			continue
		}
		remote := r.to.port(r.toPort, sourceNeighbour, r.at)
		remote.evidence[r.obs] = true
		remote.see(r.toSpelling, sourceNeighbour, r.obs)
		r.toIface = remote
		r.fromIface = local
	}
	// A second pass, so a port created by a later report is still linked to an earlier one.
	for _, r := range reports {
		if r.fromIface == nil {
			r.fromIface = r.from.ports[r.fromPort]
		}
		if r.toIface == nil && r.to != nil && r.toPort != "" {
			r.toIface = r.to.ports[r.toPort]
		}
	}
}
