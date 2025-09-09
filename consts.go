package smriti

const (
	InitialAllocationRatio = 0.20   // 20% of maxBlockCount for initial allocation
	ExpansionRatio         = 0.10   // Expand by 10% of maxBlockCount
	ShrinkRatio            = 0.10   // Shrink by 10% of maxBlockCount
	ShrinkThreshold        = 0.50   // Shrink when available blocks exceed 50% of maxBlockCount
	ShrinkTimeout          = 1 * 60 // 1 minutes (in seconds
)
