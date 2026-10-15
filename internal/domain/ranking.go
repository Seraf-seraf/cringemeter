package domain

import "math"

const TopSize = 10
const wilson95Z = 1.96

func (c Counts) WilsonLowerBound() float64 {
	total := c.Total()
	if total <= 0 || c.Cringe == 0 {
		return 0
	}
	n := float64(total)
	p := float64(c.Cringe) / n
	zSquared := wilson95Z * wilson95Z
	lower := (p + zSquared/(2*n) - wilson95Z*math.Sqrt(p*(1-p)/n+zSquared/(4*n*n))) / (1 + zSquared/n)
	return math.Max(0, lower)
}
