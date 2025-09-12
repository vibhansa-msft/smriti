package smriti

const (
	InitialAllocationRatio = 0.20   // 20% of maxBlocks for initial allocation
	ExpansionRatio         = 0.10   // Expand by 10% of maxBlocks
	ShrinkRatio            = 0.10   // Shrink by 10% of maxBlocks
	ShrinkThreshold        = 0.50   // Shrink when available blocks exceed 50% of maxBlocks
	ShrinkTimeout          = 1 * 60 // 1 minutes (in seconds
)
