package smriti

import "time"

const (
	initialAllocationRatio = 0.20             // 20% of maxBlockCount for initial allocation
	expansionThreshold     = 0.80             // If 80% or more of current allocated blocks are in use, consider expanding
	expansionRatio         = 0.10             // Expand by 10% of maxBlockCount
	shrinkThreshold        = 0.20             // If 20% or more of maxBlockCount are available (80% idle based on max capacity), consider shrinking
	shrinkRatio            = 0.10             // Shrink by 10% of maxBlockCount
	idleCheckInterval      = 5 * time.Second  // How often the monitor goroutine checks usage
	idleShrinkDelay        = 20 * time.Second // How long the system must be idle before shrinking
	getBlockTimeout        = 30 * time.Second // Timeout for GetBlock operation
)
