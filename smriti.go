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

	sync.Mutex                // Mutex to protect access to currentAllocatedCount and allMappedBlocks
	wg         sync.WaitGroup // WaitGroup to manage background goroutines

	zeroSlice []byte // Slice which holds all 0's of size blockSize
}

// NewSmriti creates and initializes a new Smriti.
// It takes the desired block size and maximum total memory size.
// It returns a pointer to the initialized Smriti or an error if initialization fails.
func New(blockSize, blockCount int) (*Smriti, error) {
	if blockSize <= 0 || blockCount <= 0 {
		return nil, fmt.Errorf("block size and block count must be non zero")
	}

	initialBlockCount := int(float64(blockCount) * InitialAllocationRatio)
	if initialBlockCount == 0 && blockCount > 0 {
		// Ensure at least one block is allocated initially if possible
		initialBlockCount = 1
	}

	// Channels are buffered with maxBlockCount capacity to prevent deadlocks
	// and allow for asynchronous operations without blocking.
	sm := &Smriti{
		blockSize:             blockSize,
		maxBlockCount:         blockCount,
		initialBlockCount:     initialBlockCount,
		currentAllocatedCount: 0, // Will be updated during initial allocation
		availableBlocks:       make(chan []byte, blockCount),
		returnedBlocks:        make(chan []byte, blockCount),
		allMappedBlocks:       make(map[uintptr][]byte),
	}

	// Perform initial allocation of blocks.
	cnt, err := sm.allocateBlocks(initialBlockCount)
	if err != nil {
		if cnt == 0 {
			return nil, fmt.Errorf("failed to pre-allocate initial blocks: %w", err)
		} else {
			// Only a partial allocation succeeded.
			return sm, fmt.Errorf("warning: only allocated %d out of %d initial blocks: %v", cnt, initialBlockCount, err)
		}
	}

	sm.zeroSlice = make([]byte, blockSize) // Slice of zeros for clearing memory

	// Start background goroutine to handle returned blocks.
	sm.wg.Add(1)
	go sm.handleReturnedBlocks()

	return sm, nil
}

// Close gracefully shuts down the Smriti, unmapping all allocated memory.
// It signals background goroutines to stop and waits for them to finish.
func (sm *Smriti) Close() {
	sm.Lock()
	defer sm.Unlock()

	// Mark as closed by closing the channel.
	close(sm.availableBlocks)
	close(sm.returnedBlocks)

	sm.wg.Wait()

	// Drain availableBlocks and returnedBlocks channels to ensure all mmap'd blocks
	// are accounted for in allMappedBlocks before unmapping.
	for block := range sm.returnedBlocks {
		sm.availableBlocks <- block // Push returned blocks back to availableBlocks
	}
	_, _ = sm.deallocateBlocks(len(sm.availableBlocks)) // Deallocate all available blocks
	sm.currentAllocatedCount = 0                        // Reset count
	sm.blockSize = 0
	sm.maxBlockCount = 0
	sm.initialBlockCount = 0
	sm.zeroSlice = nil
	sm.allMappedBlocks = nil
}

// Stats provides current statistics of the Smriti.
func (sm *Smriti) Stats() (int, int, int) {
	sm.Lock()
	defer sm.Unlock()

	return sm.currentAllocatedCount, len(sm.availableBlocks), len(sm.returnedBlocks)
}

// GetBlock provides a memory block to the client.
// It blocks until a block is available or a timeout occurs.
func (sm *Smriti) Allocate() ([]byte, error) {
	sm.Lock()
	defer sm.Unlock()

	return sm.getBlockInternal()
}

// PutBlock returns a memory block from the client to the manager.
// It attempts to push the block back into the returnedBlocks channel.
func (sm *Smriti) Free(block []byte) error {
	sm.Lock()
	defer sm.Unlock()

	return sm.putBlockInternal(block)
}

// Internal implementation of GetBlock without locking.
func (sm *Smriti) getBlockInternal() ([]byte, error) {
	select {
	case block := <-sm.availableBlocks:
		return block, nil
	default:
		// Channel is empty which means we do not have any free block
		// Check if max blocks are allocated or not,
		// if not then allocate new blocks baed on expansion ration and return
		// the block back to caller
		cnt, _ := sm.expand()
		if cnt > 0 {
			return sm.getBlockInternal()
		}
	}

	return nil, fmt.Errorf("no blocks available and cannot expand further")
}

// Internal implementation of PutBlock without locking.
func (sm *Smriti) putBlockInternal(block []byte) error {
	if block == nil {
		return fmt.Errorf("cannot return a nil block")
	}

	// Verify that the block being returned is one that was managed by this manager.
	// This prevents returning arbitrary memory or already deallocated blocks.
	addr := uintptr(unsafe.Pointer(&block[0]))
	_, ok := sm.allMappedBlocks[addr]

	if !ok {
		// Attempted to return an unknown or already deallocated block
		return fmt.Errorf("attempted to return an unknown block at %x", addr)
	}

	// Push the block to the returnedBlocks channel.
	// Use a non-blocking select in case the channel is full (which should ideally not happen
	// if the channel capacity is maxBlockCount).
	select {
	case sm.returnedBlocks <- block:
		// Successfully returned
	default:
		return fmt.Errorf("returned block channel is full, block at %x dropped", addr)
	}

	return nil
}

// handleReturnedBlocks is a background goroutine that processes blocks returned by clients.
// It immediately pushes returned blocks back to the availableBlocks channel, making them ready for reuse.
func (sm *Smriti) handleReturnedBlocks() {
	defer sm.wg.Done()

	for {
		select {
		case block, ok := <-sm.returnedBlocks:
			if !ok {
				return
			}

			sm.Lock()
			// Reset the block to avoid any corruption
			copy(block, sm.zeroSlice)

			// Move this block to available blocks for reuse
			sm.availableBlocks <- block
			sm.Unlock()

		case <-time.After(1 * time.Minute):
			sm.Lock()
			_, _ = sm.shrink()
			sm.Unlock()
		}
	}
}
