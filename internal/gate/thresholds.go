package gate

// thresholds turn a coverage fraction into a verdict. Published needs every device of the baseline
// back; the two figures are where degraded starts and where quarantine does.
type thresholds struct {
	degradedAt       float64
	quarantinedBelow float64
	source           string
}

var defaultThresholds = thresholds{degradedAt: 0.9, quarantinedBelow: 0.9, source: "default"}

// thresholdsFor takes what the snapshot's own perimeter declared, falling back to the defaults key
// by key. Reading them from the perimeter row rather than from the configuration document is what
// keeps an old verdict readable under the rules it was made with, whatever the document says now.
func thresholdsFor(s snapshot) thresholds {
	switch {
	case s.degradedAt != nil && s.quarantinedBelow != nil:
		return thresholds{*s.degradedAt, *s.quarantinedBelow, "perimeter"}
	// One declared and not the other: the other follows it. Leaving the undeclared one on its
	// default would silently cross the two (declaring degraded_at 0.5 against a default quarantine
	// at 0.9 puts half the range in both bands), which the document's own validation only catches
	// when both are written out.
	case s.degradedAt != nil:
		return thresholds{*s.degradedAt, *s.degradedAt, "perimeter"}
	case s.quarantinedBelow != nil:
		return thresholds{*s.quarantinedBelow, *s.quarantinedBelow, "perimeter"}
	}
	return defaultThresholds
}

func classify(coverage float64, t thresholds) string {
	switch {
	case coverage >= 1:
		return Published
	case coverage < t.quarantinedBelow:
		return Quarantined
	case coverage >= t.degradedAt:
		return Degraded
	default:
		return Quarantined
	}
}

func (t thresholds) record() map[string]any {
	return map[string]any{"degraded_at": t.degradedAt, "quarantined_below": t.quarantinedBelow, "source": t.source}
}
