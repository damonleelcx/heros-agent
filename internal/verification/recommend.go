package verification

// Recommendations filters a batch of verdicts down to the ones that may surface as recommendations —
// exactly those whose gate_result is `pass`. This is the load-bearing nothing-unverified guarantee
// (§4.5): the recommendation surface reads ONLY the output of this function, so a gate-failed or
// never-run proposal can never be shown by a rendering bug or an eager summary.
func Recommendations(verdicts []Verdict) []Verdict {
	out := make([]Verdict, 0, len(verdicts))
	for _, v := range verdicts {
		if v.Passed() {
			out = append(out, v)
		}
	}
	return out
}

// Withheld returns the complement — verdicts that did NOT pass. These are shown, if at all, only in a
// separate, clearly-labeled "did not pass verification" section (§6.2), never in the ranked list.
func Withheld(verdicts []Verdict) []Verdict {
	out := make([]Verdict, 0, len(verdicts))
	for _, v := range verdicts {
		if !v.Passed() {
			out = append(out, v)
		}
	}
	return out
}
