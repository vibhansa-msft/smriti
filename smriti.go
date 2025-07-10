package smriti

import (
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// Smriti manages a pool of memory blocks using mmap.
type Smriti struct {
	blockSize             int // Size of each individual memory block
	maxMemorySize         int // Maximum total memory that can be allocated
	maxBlockCount         int // Maximum number of blocks that can be allocated (maxMemorySize / blockSize)
	initialBlockCount     int // Number of blocks initially allocated (20% of maxBlockCount)
	currentAllocatedCount int // Number of blocks currently mmap'd and managed by the manager

	availableBlocks chan []byte // Channel to provide blocks to clients
	returnedBlocks  chan []byte // Channel to receive blocks back from clients

	// allMappedBlocks stores all mmap'd blocks by their base address.
	// This map is crucial for two reasons:
	// 1. It prevents the Go GC from reclaiming the []byte slice headers while the
	//    underlying mmap'd memory is still active.
	// 2. It allows us to track all active mmap'd regions so they can be properly
	//    munmap'd when shrinking or when the manager is closed.
	// The key is the uintptr (unsigned integer pointer) of the first byte of the slice.
	allMappedBlocks map[uintptr][]byte

	mu sync.Mutex // Mutex to protect access to currentAllocatedCount and allMappedBlocks

	stopSignal chan struct{}  // Channel to signal background goroutines to stop
	wg         sync.WaitGroup // WaitGroup to wait for background goroutines to finish

	idleTimer   *time.Timer   // Timer for detecting idle periods for shrinking
	idleTimeout time.Duration // Duration after which idle shrinking can occur
}

// NewSmriti creates and initializes a new Smriti.
// It takes the desired block size and maximum total memory size.
// It returns a pointer to the initialized Smriti or an error if initialization fails.
func NewSmriti(blockSize, maxMemorySize int) (*Smriti, error) {
	if blockSize <= 0 || maxMemorySize <= 0 {
		return nil, fmt.Errorf("block size and max memory size must be positive")
	}
	if maxMemorySize%blockSize != 0 {
		return nil, fmt.Errorf("max memory size (%d) must be a multiple of block size (%d)", maxMemorySize, blockSize)
	}

	maxBlockCount := maxMemorySize / blockSize
	initialBlockCount := int(float64(maxBlockCount) * initialAllocationRatio)
	if initialBlockCount == 0 && maxBlockCount > 0 {
		// Ensure at least one block is allocated initially if possible
		initialBlockCount = 1
	}

	// Channels are buffered with maxBlockCount capacity to prevent deadlocks
	// and allow for asynchronous operations without blocking.
	mm := &Smriti{
		blockSize:             blockSize,
		maxMemorySize:         maxMemorySize,
		maxBlockCount:         maxBlockCount,
		initialBlockCount:     initialBlockCount,
		currentAllocatedCount: 0, // Will be updated during initial allocation
		availableBlocks:       make(chan []byte, maxBlockCount),
		returnedBlocks:        make(chan []byte, maxBlockCount),
		allMappedBlocks:       make(map[uintptr][]byte),
		stopSignal:            make(chan struct{}),
		idleTimeout:           idleShrinkDelay,
	}

	// Perform initial allocation of blocks.
	if err := mm.allocateBlocks(initialBlockCount); err != nil {
		return nil, fmt.Errorf("failed to pre-allocate initial blocks: %w", err)
	}

	// Start background goroutines for dynamic scaling and handling returned blocks.
	mm.wg.Add(2)
	go mm.monitorAndScale()
	go mm.handleReturnedBlocks()

	return mm, nil
}

// Close gracefully shuts down the Smriti, unmapping all allocated memory.
// It signals background goroutines to stop and waits for them to finish.
func (mm *Smriti) Close() {
	close(mm.stopSignal) // Signal background goroutines to stop
	mm.wg.Wait()         // Wait for all goroutines to finish their cleanup

	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Drain availableBlocks and returnedBlocks channels to ensure all mmap'd blocks
	// are accounted for in allMappedBlocks before unmapping.
	for len(mm.availableBlocks) > 0 {
		block := <-mm.availableBlocks
		addr := uintptr(unsafe.Pointer(&block[0]))
		mm.allMappedBlocks[addr] = block // Ensure it's in the map for munmap
	}
	for len(mm.returnedBlocks) > 0 {
		block := <-mm.returnedBlocks
		addr := uintptr(unsafe.Pointer(&block[0]))
		mm.allMappedBlocks[addr] = block // Ensure it's in the map for munmap
	}

	// Unmap all remaining allocated memory regions.
	for _, block := range mm.allMappedBlocks {
		_ = free(block)
	}

	mm.allMappedBlocks = make(map[uintptr][]byte) // Clear the map
	mm.currentAllocatedCount = 0                  // Reset count
}

// GetBlock provides a memory block to the client.
// It blocks until a block is available or a timeout occurs.
func (mm *Smriti) GetBlock() ([]byte, error) {
	select {
	case block := <-mm.availableBlocks:
		return block, nil
	case <-time.After(getBlockTimeout): // Timeout for getting a block
		return nil, fmt.Errorf("timeout waiting for a memory block after %s", getBlockTimeout)
	}
}

// PutBlock returns a memory block from the client to the manager.
// It attempts to push the block back into the returnedBlocks channel.
func (mm *Smriti) PutBlock(block []byte) error {
	if block == nil {
		return fmt.Errorf("cannot return a nil block")
	}

	// Verify that the block being returned is one that was managed by this manager.
	// This prevents returning arbitrary memory or already deallocated blocks.
	mm.mu.Lock()
	addr := uintptr(unsafe.Pointer(&block[0]))
	_, ok := mm.allMappedBlocks[addr]
	mm.mu.Unlock()

	if !ok {
		// Attempted to return an unknown or already deallocated block
		return fmt.Errorf("attempted to return an unknown block at %x", addr)
	}

	// Push the block to the returnedBlocks channel.
	// Use a non-blocking select in case the channel is full (which should ideally not happen
	// if the channel capacity is maxBlockCount).
	select {
	case mm.returnedBlocks <- block:
		// Successfully returned
	default:
		return fmt.Errorf("returned block channel is full, block at %x dropped", addr)
	}

	return nil
}

// handleReturnedBlocks is a background goroutine that processes blocks returned by clients.
// It immediately pushes returned blocks back to the availableBlocks channel, making them ready for reuse.
func (mm *Smriti) handleReturnedBlocks() {
	defer mm.wg.Done()
	for {
		select {
		case block := <-mm.returnedBlocks:
			// When a block is returned, push it back to availableBlocks.
			// This is where the actual "availability" for new GetBlock calls happens.
			select {
			case mm.availableBlocks <- block:
				// Block successfully made available.
			default:
				// This case should ideally not be hit if availableBlocks capacity is maxBlockCount.
				// If it is hit, it means availableBlocks is full, which implies all blocks are already available.
				// In this scenario, we might have excess blocks that the monitorAndScale goroutine
				// should eventually deallocate. For now, we log a warning and effectively drop the block.
				_ = free(block) // Attempt to deallocate one block to prevent memory leak
			}
		case <-mm.stopSignal:
			return
		}
	}
}

// monitorAndScale continuously monitors block usage and scales allocation up or down.
// It runs in a separate goroutine.
func (mm *Smriti) monitorAndScale() {
	defer mm.wg.Done()
	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()

	// Initialize idle timer. It's stopped initially and reset when conditions for shrinking are met.
	mm.mu.Lock()
	mm.idleTimer = time.NewTimer(mm.idleTimeout)
	mm.idleTimer.Stop()
	mm.mu.Unlock()

	for {
		select {
		case <-ticker.C: // Periodically check usage
			mm.mu.Lock()
			currentAvailable := len(mm.availableBlocks)
			currentAllocated := mm.currentAllocatedCount
			mm.mu.Unlock()

			// Calculate usage percentage based on currently allocated blocks.
			// This is more accurate for expansion decisions.
			usagePercentage := 0.0
			if currentAllocated > 0 {
				usagePercentage = float64(currentAllocated-currentAvailable) / float64(currentAllocated)
			}
			// Calculate available percentage relative to max possible blocks.
			// This is more relevant for shrinking decisions.
			availablePercentageOfMax := float64(currentAvailable) / float64(mm.maxBlockCount)

			// Expansion Logic: If usage is high and we haven't reached max capacity.
			if usagePercentage >= expansionThreshold && currentAllocated < mm.maxBlockCount {
				blocksToAllocate := int(float64(mm.maxBlockCount) * expansionRatio)
				if blocksToAllocate == 0 { // Ensure at least one block is added if ratio is too small
					blocksToAllocate = 1
				}
				// Cap the allocation to not exceed maxBlockCount.
				if currentAllocated+blocksToAllocate > mm.maxBlockCount {
					blocksToAllocate = mm.maxBlockCount - currentAllocated
				}

				if blocksToAllocate > 0 {
					mm.mu.Lock()
					_ = mm.allocateBlocks(blocksToAllocate)
					mm.mu.Unlock()

					// Reset the idle timer if we just expanded, as this indicates activity.
					mm.mu.Lock()
					mm.idleTimer.Stop()
					mm.idleTimer.Reset(mm.idleTimeout) // Reset timer to prevent premature shrinking
					mm.mu.Unlock()
				}
			}

			// Shrinking Logic: If many blocks are available (relative to max) and we are above initial allocation.
			// We only *start* the idle timer if these conditions are met.
			if availablePercentageOfMax > shrinkThreshold && currentAllocated > mm.initialBlockCount {
				mm.mu.Lock()
				// If the timer is not active or has already fired, reset it.
				// This ensures we only shrink after a continuous idle period.
				if !mm.idleTimer.Stop() { // Stop returns true if timer was active
					select { // Drain channel if it fired before Stop was called
					case <-mm.idleTimer.C:
					default:
					}
				}
				mm.idleTimer.Reset(mm.idleTimeout)
				mm.mu.Unlock()
			} else {
				// If conditions for shrinking are not met, ensure the timer is stopped.
				mm.mu.Lock()
				if !mm.idleTimer.Stop() { // Stop returns true if timer was active
					select { // Drain channel if it fired
					case <-mm.idleTimer.C:
					default:
					}
				}
				mm.mu.Unlock()
			}

		case <-mm.idleTimer.C: // This case executes only when the idleTimer fires (i.e., system was idle)
			mm.mu.Lock()
			currentAvailable := len(mm.availableBlocks)
			currentAllocated := mm.currentAllocatedCount
			availablePercentageOfMax := float64(currentAvailable) / float64(mm.maxBlockCount)

			// Double-check conditions before shrinking, as state might have changed since timer was set.
			if availablePercentageOfMax > shrinkThreshold && currentAllocated > mm.initialBlockCount {
				blocksToDeallocate := int(float64(mm.maxBlockCount) * shrinkRatio)
				if blocksToDeallocate == 0 { // Ensure at least one block is deallocated if ratio is too small
					blocksToDeallocate = 1
				}
				// Cap the deallocation to not go below initialBlockCount.
				if currentAllocated-blocksToDeallocate < mm.initialBlockCount {
					blocksToDeallocate = currentAllocated - mm.initialBlockCount
				}

				if blocksToDeallocate > 0 {
					_ = mm.deallocateBlocks(blocksToDeallocate)
				}
			}
			mm.mu.Unlock()

		case <-mm.stopSignal: // Received stop signal
			// Ensure timer is stopped and drained before exiting
			mm.mu.Lock()
			if !mm.idleTimer.Stop() {
				select {
				case <-mm.idleTimer.C:
				default:
				}
			}
			mm.mu.Unlock()
			return
		}
	}
}
