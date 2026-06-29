package web

// clockParts decomposes a second count into hours, minutes, and seconds.
// Negative values are clamped to zero.
func clockParts(total int) (h, m, s int) {
	if total < 0 {
		total = 0
	}
	return total / 3600, (total % 3600) / 60, total % 60
}
