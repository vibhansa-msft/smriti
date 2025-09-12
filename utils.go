package smriti

import (
	"fmt"
	"time"
	"unsafe"
)

// allocateBlocks performs mmap for the specified number of new blocks.
// It updates currentAllocatedCount and pushes the newly allocated blocks to the available channel.
// This function must be called with sm.mu locked to protect shared state.
func (sm *Smriti[T]) allocateBlocks(count int, blockChannel chan *Sanrachna[T]) (int, error) {
	var i int = 0
	for ; i < count; i++ {
		// Use allocate for allocation.
		bytes, err := allocate(sm.blockSize) // Renamed from mmapFunc
		if err != nil {
			return i, fmt.Errorf("allocate failed for block %d: %w", i, err)
		}

		// Store the block by its base address in reference.
		// This ensures the Go runtime keeps a reference to the []byte slice header,
		// preventing it from being garbage collected while the underlying mmap'd
		// memory is still active. This is crucial for proper free later.
		addr := uintptr(unsafe.Pointer(&bytes[0]))
		sm.reference[addr] = bytes
		block := &Sanrachna[T]{bytes: bytes}

		// Push the newly allocated block to the available channel.
		select {
		case blockChannel <- block:
			sm.currentAllocatedCount++
		default:
			// This case should ideally not be hit if available capacity is maxBlocks.
			// If it is hit, it means the channel is full, which implies all blocks are already available.
			// In this rare scenario, we might have allocated a block but couldn't make it immediately available.
			// We should free it to prevent memory leak.
			free(bytes)                // Attempt to unmap the block immediately // Renamed from munmapFunc
			delete(sm.reference, addr) // Remove from tracking
			return i, fmt.Errorf("available blocks channel full during allocation, stopping further allocation")
		}
	}

	return i, nil
}

// deallocateBlocks performs free for the specified number of blocks.
// It removes blocks from the available channel and then unmaps them.
// This function must be called with sm.mu locked to protect shared state.
func (sm *Smriti[T]) deallocateBlocks(count int) (int, error) {
	var i int = 0
	for ; i < count; i++ {
		// Try to get a block from the available channel.
		// Use a non-blocking select to avoid waiting indefinitely if no blocks are available.
		select {
		case block := <-sm.available:
			addr := uintptr(unsafe.Pointer(&block.bytes[0]))

			// Perform the actual free operation.
			// Use free for deallocation.
			if err := free(block.bytes); err != nil { // Renamed from munmapFunc
				sm.available <- block // Push it back to available on failure
				return i, fmt.Errorf("free failed for block %d at address %x: %w", i, addr, err)
			}

			delete(sm.reference, addr) // Remove from our tracking map
			sm.currentAllocatedCount-- // Decrement the count of managed blocks

			block = nil
		default:
			// No available blocks to deallocate, stop trying to shrink.
			return i, nil
		}
	}

	return i, nil
}

// Expand the blocks if more blocks can be allocated
func (sm *Smriti[T]) expand() (int, error) {
	if sm.currentAllocatedCount >= sm.maxBlocks {
		return 0, fmt.Errorf("cannot expand anymore")
	}

	blocksToAllocate := int(float64(sm.maxBlocks) * ExpansionRatio)
	if blocksToAllocate == 0 { // Ensure at least one block is added if ratio is too small
		blocksToAllocate = 1
	}

	blocksToAllocate = min(blocksToAllocate, sm.maxBlocks-sm.currentAllocatedCount)
	sm.lastExpansonTime = time.Now()
	return sm.allocateBlocks(blocksToAllocate, sm.available)
}

// Shrink the blocks if more blocks can be deallocated
func (sm *Smriti[T]) shrink() (int, error) {
	// If 50% of allocated blocks are free then only we go for shrink
	freeBlocks := len(sm.available)
	if freeBlocks > int(float64(sm.currentAllocatedCount)*ShrinkThreshold) {
		blocksToDeallocate := int(float64(sm.currentAllocatedCount) * ShrinkRatio)
		if blocksToDeallocate == 0 { // Ensure at least one block is removed if ratio is too small
			blocksToDeallocate = 1
		}

		blocksToDeallocate = min(blocksToDeallocate, sm.currentAllocatedCount, freeBlocks-sm.initialCount)
		if blocksToDeallocate > 0 {
			return sm.deallocateBlocks(blocksToDeallocate)
		}
	}

	return 0, nil
}
