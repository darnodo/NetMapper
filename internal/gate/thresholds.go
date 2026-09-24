package gate

// thresholds turn a coverage fraction into a verdict. Published needs every device of the baseline
// back, which FR-005 fixes; degradedAt is therefore the only boundary a perimeter places, the one
// between degraded and quarantined.
type thresholds struct {
	degradedAt float64
	source     string
}

var defaultThresholds = thresholds{degradedAt: 0.9, source: "default"}

// thresholdsFor takes what the snapshot's own perimeter declared, or the default when it declared
// none. Reading it from the perimeter row rather than from the configuration document is what keeps
// an old verdict readable under the rule it was made with, whatever the document says now.
func thresholdsFor(s snapshot) thresholds {
	if s.degradedAt != nil {
		return thresholds{degradedAt: *s.degradedAt, source: "perimeter"}
	}
	return defaultThresholds
}

func classify(coverage float64, t thresholds) string {
	switch {
	case coverage >= 1:
		return Published
	case coverage >= t.degradedAt:
		return Degraded
	default:
		return Quarantined
	}
}

func (t thresholds) record() map[string]any {
	return map[string]any{"degraded_at": t.degradedAt, "source": t.source}
}
