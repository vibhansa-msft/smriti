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
	maxBlocks             int // Maximum number of blocks that can be allocated (maxMemorySize / blockSize)
	initialCount          int // Number of blocks initially allocated (20% of maxBlocks)
	reservedCount         int // Number of blocks reserved for high-priority allocations
	currentAllocatedCount int // Number of blocks currently mmap'd and managed by the manager

	available chan []byte // Channel to provide blocks to clients
	reserved  chan []byte // Channel to provide blocks to clients
	returned  chan []byte // Channel to receive blocks back from clients

	// reference stores all mmap'd blocks by their base address.
	// This map is crucial for two reasons:
	// 1. It prevents the Go GC from reclaiming the []byte slice headers while the
	//    underlying mmap'd memory is still active.
	// 2. It allows us to track all active mmap'd regions so they can be properly
	//    munmap'd when shrinking or when the manager is closed.
	// The key is the uintptr (unsigned integer pointer) of the first byte of the slice.
	reference map[uintptr][]byte

	sync.Mutex                // Mutex to protect access to currentAllocatedCount and reference
	wg         sync.WaitGroup // WaitGroup to manage background goroutines
	done       chan struct{}  // Channel to signal shutdown of background goroutines

	zeroSlice        []byte    // Slice which holds all 0's of size blockSize
	lastExpansonTime time.Time // Timestamp of the last allocation
}

// NewSmriti creates and initializes a new Smriti.
// It takes the desired block size and maximum total memory size.
// It returns a pointer to the initialized Smriti or an error if initialization fails.
func NewSmriti(blockSize, blockCount int, reservePercent int) (*Smriti, error) {
	if blockSize <= 0 || blockCount <= 0 {
		return nil, fmt.Errorf("block size and block count must be non zero")
	}

	if reservePercent < 0 || reservePercent >= 50 {
		return nil, fmt.Errorf("reserve percent must be between 0 and 49")
	}

	initialCount := int(float64(blockCount) * InitialAllocationRatio)
	if initialCount == 0 && blockCount > 0 {
		// Ensure at least one block is allocated initially if possible
		initialCount = 1
	}

	reservedCount := (blockCount * reservePercent) / 100
	if reservedCount == 0 && reservePercent > 0 {
		// Ensure at least one block is reserved if reservePercent > 0
		reservedCount = 1
	}

	// Channels are buffered with maxBlocks capacity to prevent deadlocks
	// and allow for asynchronous operations without blocking.
	sm := &Smriti{
		blockSize:             blockSize,
		maxBlocks:             blockCount,
		initialCount:          initialCount + reservedCount,
		reservedCount:         reservedCount,
		currentAllocatedCount: 0, // Will be updated during initial allocation
		available:             make(chan []byte, blockCount),
		reserved:              make(chan []byte, reservedCount),
		returned:              make(chan []byte, blockCount),
		reference:             make(map[uintptr][]byte),
		lastExpansonTime:      time.Now(),
		done:                  make(chan struct{}),
	}

	// Perform initial allocation of blocks.
	cnt, err := sm.allocateBlocks(initialCount, sm.available)
	if err != nil {
		if cnt == 0 {
			return nil, fmt.Errorf("failed to pre-allocate initial blocks: %w", err)
		} else {
			// Only a partial allocation succeeded.
			return sm, fmt.Errorf("warning: only allocated %d out of %d initial blocks: %v", cnt, initialCount, err)
		}
	}

	if sm.reservedCount > 0 {
		cnt, err = sm.allocateBlocks(sm.reservedCount, sm.reserved)
		if err != nil {
			if cnt == 0 {
				return nil, fmt.Errorf("failed to pre-allocate reserved blocks: %w", err)
			} else {
				// Only a partial allocation succeeded.
				return sm, fmt.Errorf("warning: only allocated %d out of %d reserved blocks: %v", cnt, initialCount, err)
			}
		}
	}

	sm.zeroSlice = make([]byte, blockSize) // Slice of zeros for clearing memory

	// Start background goroutine to handle returned blocks.
	sm.wg.Add(1)
	go sm.handlereturned()

	return sm, nil
}

// Close gracefully shuts down the Smriti, unmapping all allocated memory.
// It signals background goroutines to stop and waits for them to finish.
func (sm *Smriti) Close() {
	// Mark shutdown and close the returned block channel
	sm.Lock()
	close(sm.done)
	close(sm.returned)
	sm.Unlock()

	// Wait for background goroutines to finish
	sm.wg.Wait()

	// Drain available and returned channels to ensure all mmap'd blocks
	// are accounted for in reference before unmapping.
	for block := range sm.returned {
		select {
		case sm.available <- block: // Push returned blocks back to available
		default:
			// If available is full, we can drop the block as it will be unmapped below
		}
	}

	// Also drain reserved
	close(sm.reserved)
	if sm.reservedCount > 0 {
		for block := range sm.reserved {
			select {
			case sm.available <- block: // Push reserved blocks back to available
			default:
				// If available is full, we can drop the block as it will be unmapped below
			}
		}
	}

	// Mark as closed by closing the channel.
	close(sm.available)

	_, _ = sm.deallocateBlocks(len(sm.available)) // Deallocate all available blocks
	sm.currentAllocatedCount = 0                  // Reset count
	sm.blockSize = 0
	sm.maxBlocks = 0
	sm.initialCount = 0
	sm.zeroSlice = nil
	sm.reference = nil
}

// Stats provides current statistics of the Smriti.
func (sm *Smriti) Stats() (int, int, int) {
	sm.Lock()
	defer sm.Unlock()

	return sm.currentAllocatedCount, len(sm.available), len(sm.returned)
}

// GetBlock provides a memory block to the client.
// It blocks until a block is available or a timeout occurs.
func (sm *Smriti) Allocate() ([]byte, error) {
	sm.Lock()
	defer sm.Unlock()

	return sm.getBlock()
}

func (sm *Smriti) AllocateReserved() ([]byte, error) {
	if sm.reservedCount == 0 {
		return nil, fmt.Errorf("no reserved blocks configured")
	}

	sm.Lock()
	defer sm.Unlock()

	blk, err := sm.getReservedBlock()
	if err != nil {
		// If no reserved blocks are available, fallback to regular allocation
		return sm.getBlock()
	}

	return blk, err
}

// PutBlock returns a memory block from the client to the manager.
// It attempts to push the block back into the returned channel.
func (sm *Smriti) Free(block []byte) error {
	if len(block) != sm.blockSize {
		return fmt.Errorf("block size mismatch: expected %d, got %d", sm.blockSize, len(block))
	}

	sm.Lock()
	defer sm.Unlock()

	return sm.putBlock(block)
}

// Internal implementation of GetBlock without locking.
func (sm *Smriti) getBlock() ([]byte, error) {
	select {
	case block := <-sm.available:
		return block, nil
	default:
		// Channel is empty which means we do not have any free block
		// Check if max blocks are allocated or not,
		// if not then allocate new blocks baed on expansion ration and return
		// the block back to caller
		cnt, _ := sm.expand()
		if cnt > 0 {
			return sm.getBlock()
		}
	}

	return nil, fmt.Errorf("no blocks available and cannot expand further")
}

// Internal implementation of GetBlockFromReserved without locking.
func (sm *Smriti) getReservedBlock() ([]byte, error) {
	select {
	case block := <-sm.reserved:
		return block, nil
	default:
		// Channel is empty which means we do not have any free block
		return nil, fmt.Errorf("no reserved blocks available")
	}
}

// Internal implementation of PutBlock without locking.
func (sm *Smriti) putBlock(block []byte) error {
	if block == nil {
		return fmt.Errorf("cannot return a nil block")
	}

	// Verify that the block being returned is one that was managed by this manager.
	// This prevents returning arbitrary memory or already deallocated blocks.
	addr := uintptr(unsafe.Pointer(&block[0]))
	_, ok := sm.reference[addr]

	if !ok {
		// Attempted to return an unknown or already deallocated block
		return fmt.Errorf("attempted to return an unknown block at %x", addr)
	}

	// Push the block to the returned channel.
	// Use a non-blocking select in case the channel is full (which should ideally not happen
	// if the channel capacity is maxBlocks).
	select {
	case sm.returned <- block:
		// Successfully returned
	default:
		panic("returned channel is full, cannot return block (should not happen)")
	}

	return nil
}

// handlereturned is a background goroutine that processes blocks returned by clients.
// It immediately pushes returned blocks back to the available channel, making them ready for reuse.
func (sm *Smriti) handlereturned() {
	defer sm.wg.Done()

	for {
		select {
		case block, ok := <-sm.returned:
			if !ok {
				return
			}

			// Reset the block to avoid any corruption
			copy(block, sm.zeroSlice)

			if sm.reservedCount <= 0 {
				sm.available <- block
			} else {
				// Rserve pool is configured so we need to fill that first
				select {
				case sm.reserved <- block:
					// Successfully returned to reserved pool
				default:
					// Reserved pool full, try normal pool
					select {
					case sm.available <- block:
						// Successfully returned to normal pool
					default:
						// Both pools full, drop the block (should not happen if channels are sized correctly)
						// Panic in this condition as this should never happen
						panic("both available and reserved block channels are full, cannot return block")
					}
				}
			}

		case <-time.After(ShrinkTimeout * time.Second):
			// If last allocation was just done then we shall not shring the pool
			// Also, shrink the pool only when we have more than initial blocks available
			sm.Lock()
			if time.Since(sm.lastExpansonTime) > (ShrinkTimeout*time.Second) &&
				len(sm.available) > sm.initialCount {
				_, _ = sm.shrink()

			}
			sm.Unlock()

		case <-sm.done:
			return
		}
	}
}
