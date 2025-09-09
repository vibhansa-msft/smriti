package smriti

import (
	"fmt"
	"time"
	"unsafe"
)

// allocateBlocks performs mmap for the specified number of new blocks.
// It updates currentAllocatedCount and pushes the newly allocated blocks to the availableBlocks channel.
// This function must be called with sm.mu locked to protect shared state.
func (sm *Smriti) allocateBlocks(count int) (int, error) {
	var i int = 0
	for ; i < count; i++ {
		// Use allocate for allocation.
		block, err := allocate(sm.blockSize) // Renamed from mmapFunc
		if err != nil {
			return i, fmt.Errorf("allocate failed for block %d: %w", i, err)
		}

		// Store the block by its base address in allMappedBlocks.
		// This ensures the Go runtime keeps a reference to the []byte slice header,
		// preventing it from being garbage collected while the underlying mmap'd
		// memory is still active. This is crucial for proper free later.
		addr := uintptr(unsafe.Pointer(&block[0]))
		sm.allMappedBlocks[addr] = block

		// Push the newly allocated block to the availableBlocks channel.
		select {
		case sm.availableBlocks <- block:
			sm.currentAllocatedCount++
		default:
			// This case should ideally not be hit if availableBlocks capacity is maxBlockCount.
			// If it is hit, it means the channel is full, which implies all blocks are already available.
			// In this rare scenario, we might have allocated a block but couldn't make it immediately available.
			// We should free it to prevent memory leak.
			free(block)                      // Attempt to unmap the block immediately // Renamed from munmapFunc
			delete(sm.allMappedBlocks, addr) // Remove from tracking
			return i, fmt.Errorf("available blocks channel full during allocation, stopping further allocation")
		}
	}

	return i, nil
}

// deallocateBlocks performs free for the specified number of blocks.
// It removes blocks from the availableBlocks channel and then unmaps them.
// This function must be called with sm.mu locked to protect shared state.
func (sm *Smriti) deallocateBlocks(count int) (int, error) {
	var i int = 0
	for ; i < count; i++ {
		// Try to get a block from the availableBlocks channel.
		// Use a non-blocking select to avoid waiting indefinitely if no blocks are available.
		select {
		case block := <-sm.availableBlocks:
			addr := uintptr(unsafe.Pointer(&block[0]))
			if _, ok := sm.allMappedBlocks[addr]; !ok {
				continue // Skip this block if it's not tracked
			}

			// Perform the actual free operation.
			// Use free for deallocation.
			if err := free(block); err != nil { // Renamed from munmapFunc
				sm.availableBlocks <- block // Push it back to availableBlocks on failure
				return i, fmt.Errorf("free failed for block %d at address %x: %w", i, addr, err)
			}

			delete(sm.allMappedBlocks, addr) // Remove from our tracking map
			sm.currentAllocatedCount--       // Decrement the count of managed blocks
		default:
			// No available blocks to deallocate, stop trying to shrink.
			return i, nil
		}
	}

	return i, nil
}

// Expand the blocks if more blocks can be allocated
func (sm *Smriti) expand() (int, error) {
	if sm.currentAllocatedCount >= sm.maxBlockCount {
		return 0, fmt.Errorf("cannot expand anymore")
	}

	blocksToAllocate := int(float64(sm.maxBlockCount) * ExpansionRatio)
	if blocksToAllocate == 0 { // Ensure at least one block is added if ratio is too small
		blocksToAllocate = 1
	}

	blocksToAllocate = min(blocksToAllocate, sm.maxBlockCount-sm.currentAllocatedCount)
	sm.lastExpansonTime = time.Now()
	return sm.allocateBlocks(blocksToAllocate)
}

// Shrink the blocks if more blocks can be deallocated
func (sm *Smriti) shrink() (int, error) {
	// If 50% of allocated blocks are free then only we go for shrink
	freeBlocks := len(sm.availableBlocks)
	if freeBlocks > int(float64(sm.currentAllocatedCount)*ShrinkThreshold) {
		blocksToDeallocate := int(float64(sm.currentAllocatedCount) * ShrinkRatio)
		if blocksToDeallocate == 0 { // Ensure at least one block is removed if ratio is too small
			blocksToDeallocate = 1
		}

		blocksToDeallocate = min(blocksToDeallocate, sm.currentAllocatedCount, freeBlocks-sm.initialBlockCount)
		if blocksToDeallocate > 0 {
			return sm.deallocateBlocks(blocksToDeallocate)
		}
	}

	return 0, nil
}
