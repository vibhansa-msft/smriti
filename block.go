package smriti

import (
	"fmt"
	"log"
	"unsafe"
)

// allocateBlocks performs mmap for the specified number of new blocks.
// It updates currentAllocatedCount and pushes the newly allocated blocks to the availableBlocks channel.
// This function must be called with mm.mu locked to protect shared state.
func (mm *Smriti) allocateBlocks(count int) error {
	for i := 0; i < count; i++ {
		if mm.currentAllocatedCount >= mm.maxBlockCount {
			log.Println("Cannot allocate more blocks: already reached max block count.")
			return nil // Reached maximum, no error to return
		}

		block, err := allocate(mm.blockSize)
		if err != nil {
			return fmt.Errorf("mmap failed for block %d: %w", i, err)
		}

		// Store the block by its base address in allMappedBlocks.
		// This ensures the Go runtime keeps a reference to the []byte slice header,
		// preventing it from being garbage collected while the underlying mmap'd
		// memory is still active. This is crucial for proper munmap later.
		addr := uintptr(unsafe.Pointer(&block[0]))
		mm.allMappedBlocks[addr] = block

		// Push the newly allocated block to the availableBlocks channel.
		select {
		case mm.availableBlocks <- block:
			mm.currentAllocatedCount++
			log.Printf("Allocated new block. Current managed blocks: %d/%d (available: %d)\n",
				mm.currentAllocatedCount, mm.maxBlockCount, len(mm.availableBlocks))
		default:
			// This case should ideally not be hit if availableBlocks capacity is maxBlockCount.
			// If it is hit, it means the channel is full, which implies all blocks are already available.
			// In this rare scenario, we might have allocated a block but couldn't make it immediately available.
			// We should munmap it to prevent memory leak.
			log.Printf("Warning: Available blocks channel is full during allocation, munmapping block at %x.\n", addr)
			free(block)                      // Attempt to unmap the block immediately
			delete(mm.allMappedBlocks, addr) // Remove from tracking
			return fmt.Errorf("available blocks channel full during allocation, stopping further allocation")
		}
	}
	return nil
}

// deallocateBlocks performs munmap for the specified number of blocks.
// It removes blocks from the availableBlocks channel and then unmaps them.
// This function must be called with mm.mu locked to protect shared state.
func (mm *Smriti) deallocateBlocks(count int) error {
	for i := 0; i < count; i++ {
		if mm.currentAllocatedCount <= mm.initialBlockCount {
			log.Println("Cannot deallocate more blocks: already at initial block count.")
			return nil // Reached minimum, no error to return
		}

		// Try to get a block from the availableBlocks channel.
		// Use a non-blocking select to avoid waiting indefinitely if no blocks are available.
		select {
		case block := <-mm.availableBlocks:
			addr := uintptr(unsafe.Pointer(&block[0]))
			if _, ok := mm.allMappedBlocks[addr]; !ok {
				log.Printf("Warning: Attempted to deallocate unknown block at %x. Skipping.\n", addr)
				continue // Skip this block if it's not tracked
			}

			// Perform the actual munmap operation.
			if err := free(block); err != nil {
				log.Printf("Failed to munmap block at %x: %v\n", addr, err)
				// Even if munmap fails, we remove it from our tracking to avoid re-attempting
				// and to reflect that we tried to deallocate it.
			} else {
				log.Printf("Deallocated block at %x. Current managed blocks: %d/%d (available: %d)\n",
					addr, mm.currentAllocatedCount-1, mm.maxBlockCount, len(mm.availableBlocks))
			}
			delete(mm.allMappedBlocks, addr) // Remove from our tracking map
			mm.currentAllocatedCount--       // Decrement the count of managed blocks
		default:
			// No available blocks to deallocate, stop trying to shrink.
			log.Println("No available blocks in channel to deallocate, stopping shrink operation.")
			return nil
		}
	}
	return nil
}
