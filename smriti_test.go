package smriti

import (
	"log"
	"time"
)

func testSmirit() {
	// Example usage of the Smriti
	blockSize := 4096            // 4KB per block
	maxMemory := 4 * 1024 * 1024 // 4MB total memory limit

	mm, err := NewSmriti(blockSize, maxMemory)
	if err != nil {
		log.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close() // Ensure the manager is closed and memory unmapped on exit

	log.Println("Memory manager created. Simulating client usage...")

	// Simulate client getting and putting blocks
	var activeBlocks [][]byte                                  // Slice to hold blocks currently "in use" by the client
	numBlocksToAcquire := int(float64(mm.maxBlockCount) * 0.9) // Acquire 90% of max blocks to trigger expansion

	log.Printf("Client starting to acquire %d blocks...\n", numBlocksToAcquire)
	for i := 0; i < numBlocksToAcquire; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			log.Printf("Client failed to get block %d: %v\n", i, err)
			break
		}
		activeBlocks = append(activeBlocks, block)
		// Simulate some work with the block
		block[0] = byte(i % 256) // Write a byte to the block
		if i%50 == 0 {
			log.Printf("Client got block %d. Total active blocks: %d\n", i, len(activeBlocks))
		}
		time.Sleep(5 * time.Millisecond) // Simulate processing time
	}

	log.Printf("Client acquired %d blocks. Waiting for potential expansion to stabilize...\n", len(activeBlocks))
	time.Sleep(10 * time.Second) // Give some time for the monitorAndScale goroutine to react and expand

	log.Println("Client starting to return blocks...")
	for i, block := range activeBlocks {
		mm.PutBlock(block)
		if i%50 == 0 {
			log.Printf("Client returned block %d.\n", i)
		}
		time.Sleep(2 * time.Millisecond) // Simulate processing time
	}
	activeBlocks = nil // Clear the slice after returning all blocks

	log.Println("All blocks returned. Waiting for shrinking (20 seconds idle period required)...")
	time.Sleep(30 * time.Second) // Give ample time for the idle timer to fire and shrinking to occur

	log.Println("Simulation finished.")
}
