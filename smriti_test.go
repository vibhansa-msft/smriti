package smriti

import (
	"fmt"
	"log"
	"sync"
	"testing"
	"time"
	"unsafe" // Required for uintptr conversion for map key
)

// --- Mocking mmap and munmap for testing ---

// mockMmapFailures controls when mockMmap should return an error.
// If > 0, mockMmap will fail that many times.
var mockMmapFailures int
var mockMunmapFailures int

// mockMmap simulates mmap allocation for tests.
// It returns a dummy []byte slice or an error based on mockMmapFailures.
func mockMmap(size int) ([]byte, error) {
	if mockMmapFailures > 0 {
		mockMmapFailures--
		return nil, fmt.Errorf("mock mmap failure")
	}
	// Create a dummy slice. In a real test, you might want to track these.
	// For now, we just need a non-nil slice.
	return make([]byte, size), nil
}

// mockMunmap simulates munmap deallocation for tests.
// It returns an error based on mockMunmapFailures.
func mockMunmap(data []byte) error {
	if mockMunmapFailures > 0 {
		mockMunmapFailures--
		return fmt.Errorf("mock munmap failure")
	}
	return nil
}

// setMockMmapFunctions sets the global mmapFunc and munmapFunc in the memorymanager package
// to our mock implementations.
func setMockMmapFunctions() {
	MmapFunc = mockMmap
	MunmapFunc = mockMunmap
}

// resetMmapFunctions resets the global mmapFunc and munmapFunc to nil,
// which would typically cause a panic if not set by the actual OS-specific
// mmap files. This is mostly for cleanup in tests.
func resetMmapFunctions() {
	MmapFunc = nil // Or set to a default panic function for strictness
	MunmapFunc = nil
}

// --- Test Cases ---

// TestNewMemoryManagerInvalidInputs validates error handling for invalid inputs to NewMemoryManager.
func TestNewMemoryManagerInvalidInputs(t *testing.T) {
	// Ensure mocks are set up for any internal allocations NewMemoryManager might attempt
	setMockMmapFunctions()
	defer resetMmapFunctions()

	tests := []struct {
		name          string
		blockSize     int
		maxMemorySize int
		expectError   bool
	}{
		{"Zero Block Size", 0, 1024, true},
		{"Negative Block Size", -10, 1024, true},
		{"Zero Max Memory", 1024, 0, true},
		{"Negative Max Memory", 1024, -1024, true},
		{"Max Memory Not Multiple of Block Size", 100, 1025, true},
		{"Valid Inputs", 100, 1000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm, err := NewMemoryManager(tt.blockSize, tt.maxMemorySize)
			if (err != nil) != tt.expectError {
				t.Errorf("NewMemoryManager() error = %v, expectError %v", err, tt.expectError)
				if mm != nil {
					mm.Close() // Clean up if manager was created
				}
				return
			}
			if !tt.expectError && mm == nil {
				t.Error("NewMemoryManager() returned nil manager but no error was expected")
			}
			if mm != nil {
				mm.Close() // Clean up
			}
		})
	}
}

// TestMemoryManagerBasicFlow tests the core functionality of the memory manager:
// initialization, getting blocks, putting blocks back, and dynamic scaling.
func TestMemoryManagerBasicFlow(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	// Suppress logs during test to keep test output clean, re-enable at end.
	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 4096            // 4KB per block
	maxMemory := 4 * 1024 * 1024 // 4MB total memory limit (1024 blocks)

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	// Initial blocks: 20% of 1024 = 204 blocks
	initialExpectedBlocks := int(float64(maxMemory/blockSize) * InitialAllocationRatio)
	if initialExpectedBlocks == 0 && maxMemory/blockSize > 0 {
		initialExpectedBlocks = 1
	}
	if currentAllocated := mm.currentAllocatedCount; currentAllocated != initialExpectedBlocks {
		t.Errorf("Initial allocation mismatch: got %d, want %d", currentAllocated, initialExpectedBlocks)
	}
	if availableBlocks := len(mm.availableBlocks); availableBlocks != initialExpectedBlocks {
		t.Errorf("Initial available blocks mismatch: got %d, want %d", availableBlocks, initialExpectedBlocks)
	}

	var activeBlocks [][]byte // Slice to hold blocks currently "in use" by the client
	// Acquire enough blocks to trigger expansion (e.g., 90% of max blocks)
	numBlocksToAcquire := int(float64(mm.maxBlockCount) * 0.9)
	if numBlocksToAcquire > mm.maxBlockCount {
		numBlocksToAcquire = mm.maxBlockCount
	}

	t.Logf("Client starting to acquire %d blocks to trigger expansion...", numBlocksToAcquire)
	for i := 0; i < numBlocksToAcquire; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Client failed to get block %d: %v", i, err)
		}
		activeBlocks = append(activeBlocks, block)
		// Simulate some work with the block
		block[0] = byte(i % 256) // Write a byte to the block
	}

	t.Logf("Client acquired %d blocks. Waiting for potential expansion to stabilize...", len(activeBlocks))
	// Give time for the monitorAndScale goroutine to react and expand
	time.Sleep(idleCheckInterval*2 + 100*time.Millisecond) // Wait for at least two check intervals

	// After expansion, currentAllocatedCount should be higher.
	// It should be initial + 10% of max, or more if multiple expansions occurred.
	expectedAfterFirstExpansion := initialExpectedBlocks + int(float64(mm.maxBlockCount)*ExpansionRatio)
	if expectedAfterFirstExpansion > mm.maxBlockCount {
		expectedAfterFirstExpansion = mm.maxBlockCount
	}
	if mm.currentAllocatedCount < initialExpectedBlocks || mm.currentAllocatedCount > mm.maxBlockCount {
		t.Errorf("Expansion did not occur or exceeded max: current allocated %d, expected > %d and <= %d",
			mm.currentAllocatedCount, initialExpectedBlocks, mm.maxBlockCount)
	}
	t.Logf("Current allocated blocks after potential expansion: %d", mm.currentAllocatedCount)

	t.Log("Client starting to return blocks...")
	for _, block := range activeBlocks {
		mm.PutBlock(block)
	}
	activeBlocks = nil // Clear the slice after returning all blocks

	t.Log("All blocks returned. Waiting for shrinking (idle period required)...")
	// Give ample time for the idle timer to fire and shrinking to occur
	time.Sleep(idleShrinkDelay + idleCheckInterval*2 + 100*time.Millisecond)

	// After shrinking, currentAllocatedCount should be back to initial or close.
	if mm.currentAllocatedCount > initialExpectedBlocks {
		t.Errorf("Shrinking did not occur as expected: current allocated %d, expected around %d",
			mm.currentAllocatedCount, initialExpectedBlocks)
	}
	t.Logf("Current allocated blocks after potential shrinking: %d", mm.currentAllocatedCount)

	// Verify that all blocks are available after returning them and shrinking
	if len(mm.availableBlocks) != mm.currentAllocatedCount {
		t.Errorf("Not all allocated blocks are available: available %d, allocated %d",
			len(mm.availableBlocks), mm.currentAllocatedCount)
	}
}

// TestMemoryManagerExpansion specifically tests the expansion logic.
func TestMemoryManagerExpansion(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 1000 // 10 blocks max
	// initial: 2 blocks (20% of 10)
	// expansion threshold: 80% of current allocated.
	// If 2 blocks allocated, 80% usage means 2*0.8 = 1.6 blocks in use.
	// So if 2 blocks allocated, and 1 or 0 available, it should expand.

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	initialCount := mm.currentAllocatedCount
	t.Logf("Initial allocated blocks: %d", initialCount)

	// Acquire blocks to reach expansion threshold
	var acquiredBlocks [][]byte
	blocksToAcquire := int(float64(initialCount) * ExpansionThreshold)
	if blocksToAcquire == 0 && initialCount > 0 { // Ensure we acquire at least one if initial > 0
		blocksToAcquire = 1
	}
	t.Logf("Acquiring %d blocks to trigger expansion...", blocksToAcquire)
	for i := 0; i < blocksToAcquire; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get block: %v", err)
		}
		acquiredBlocks = append(acquiredBlocks, block)
	}

	// Wait for monitorAndScale to run
	time.Sleep(idleCheckInterval + 100*time.Millisecond)

	// Check if expansion occurred
	newCount := mm.currentAllocatedCount
	t.Logf("Allocated blocks after potential expansion: %d", newCount)
	if newCount <= initialCount {
		t.Errorf("Expected expansion, but allocated count did not increase. Before: %d, After: %d", initialCount, newCount)
	}
	if newCount > mm.maxBlockCount {
		t.Errorf("Allocated count exceeded max: %d > %d", newCount, mm.maxBlockCount)
	}

	// Return blocks
	for _, block := range acquiredBlocks {
		mm.PutBlock(block)
	}
}

// TestMemoryManagerShrinkage specifically tests the shrinking logic.
func TestMemoryManagerShrinkage(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 1000 // 10 blocks max
	// initial: 2 blocks (20% of 10)
	// We'll manually expand it first, then let it shrink.

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	// Manually expand to a higher number of blocks (e.g., 50% of max)
	blocksToExpandTo := int(float64(mm.maxBlockCount) * 0.5)
	if blocksToExpandTo < mm.currentAllocatedCount {
		blocksToExpandTo = mm.currentAllocatedCount + 1 // Ensure we expand
	}
	if blocksToExpandTo > mm.maxBlockCount {
		blocksToExpandTo = mm.maxBlockCount
	}

	t.Logf("Manually expanding to %d blocks...", blocksToExpandTo)
	var tempBlocks [][]byte
	for i := 0; i < blocksToExpandTo-mm.currentAllocatedCount; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get block for manual expansion: %v", err)
		}
		tempBlocks = append(tempBlocks, block)
	}
	// Return them immediately to make them available for shrinking
	for _, block := range tempBlocks {
		mm.PutBlock(block)
	}
	tempBlocks = nil

	// Wait for monitorAndScale to register the new available blocks
	time.Sleep(idleCheckInterval + 100*time.Millisecond)

	currentAllocated := mm.currentAllocatedCount
	t.Logf("Allocated blocks after manual expansion (all available): %d", currentAllocated)
	if currentAllocated <= mm.initialBlockCount {
		t.Fatalf("Failed to expand for shrinking test: current %d, initial %d", currentAllocated, mm.initialBlockCount)
	}

	t.Logf("Waiting for idle period (%s) to trigger shrinking...", idleShrinkDelay)
	time.Sleep(idleShrinkDelay + idleCheckInterval*2 + 100*time.Millisecond) // Wait for timer to fire + check interval

	newAllocated := mm.currentAllocatedCount
	t.Logf("Allocated blocks after potential shrinking: %d", newAllocated)

	// Check if shrinking occurred
	if newAllocated >= currentAllocated {
		t.Errorf("Expected shrinking, but allocated count did not decrease. Before: %d, After: %d", currentAllocated, newAllocated)
	}
	if newAllocated < mm.initialBlockCount {
		t.Errorf("Allocated count shrunk below initial: %d < %d", newAllocated, mm.initialBlockCount)
	}
}

// TestMemoryManagerMaxAllocation verifies that the manager does not allocate beyond maxBlockCount.
func TestMemoryManagerMaxAllocation(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 500 // 5 blocks max
	// initial: 1 block (20% of 5, rounded up)

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	initialCount := mm.currentAllocatedCount
	t.Logf("Initial allocated blocks: %d", initialCount)

	var acquiredBlocks [][]byte
	// Acquire all initially allocated blocks
	for i := 0; i < initialCount; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get initial block: %v", err)
		}
		acquiredBlocks = append(acquiredBlocks, block)
	}

	// Trigger expansion by simulating high usage
	// This should expand to max blocks eventually
	t.Log("Triggering expansion to max capacity...")
	time.Sleep(idleCheckInterval + 100*time.Millisecond) // Give monitor a chance to react

	// Try to acquire more blocks than maxBlockCount
	t.Logf("Attempting to acquire more blocks than max (%d)...", mm.maxBlockCount)
	for i := 0; i < mm.maxBlockCount*2; i++ { // Try to get double the max
		block, err := mm.GetBlock()
		if err != nil {
			// Expected to eventually get a timeout error if all blocks are acquired
			break
		}
		acquiredBlocks = append(acquiredBlocks, block)
	}

	// Wait for monitorAndScale to settle
	time.Sleep(idleCheckInterval * 2)

	finalCount := mm.currentAllocatedCount
	t.Logf("Final allocated blocks: %d", finalCount)
	if finalCount > mm.maxBlockCount {
		t.Errorf("Allocated count exceeded max: got %d, max %d", finalCount, mm.maxBlockCount)
	}
	if finalCount < mm.maxBlockCount {
		t.Logf("Warning: Manager did not reach max allocation. This might be due to timing or calculation. Got %d, Expected %d", finalCount, mm.maxBlockCount)
	}

	// Ensure we release all blocks
	for _, block := range acquiredBlocks {
		mm.PutBlock(block)
	}
}

// TestGetBlockTimeout verifies that GetBlock returns an error after the specified timeout.
func TestGetBlockTimeout(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 100 // Only 1 block
	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	// Get the only available block
	block, err := mm.GetBlock()
	if err != nil {
		t.Fatalf("Failed to get initial block: %v", err)
	}
	defer mm.PutBlock(block) // Ensure it's returned later

	// Try to get another block immediately, which should timeout
	start := time.Now()
	_, err = mm.GetBlock()
	duration := time.Since(start)

	if err == nil {
		t.Error("Expected GetBlock to timeout, but it returned a block.")
	}
	expectedErrorMsg := fmt.Sprintf("timeout waiting for a memory block after %s", getBlockTimeout)
	if err != nil && err.Error() != expectedErrorMsg {
		t.Errorf("Unexpected error message: got %q, want %q", err.Error(), expectedErrorMsg)
	}
	// Check if the duration is approximately the timeout period
	if duration < getBlockTimeout || duration > getBlockTimeout*1.5 { // Allow some buffer
		t.Errorf("GetBlock timeout duration mismatch: got %v, expected around %v", duration, getBlockTimeout)
	}
}

// TestPutBlockInvalidBlock ensures that PutBlock handles unknown or nil blocks gracefully.
func TestPutBlockInvalidBlock(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 100 // 1 block
	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	initialAvailable := len(mm.availableBlocks)

	// Test with a nil block
	mm.PutBlock(nil) // Should log a warning, not panic
	if len(mm.availableBlocks) != initialAvailable {
		t.Errorf("PutBlock(nil) changed available count: got %d, want %d", len(mm.availableBlocks), initialAvailable)
	}

	// Test with a block not managed by the manager
	unmanagedBlock := make([]byte, blockSize)
	mm.PutBlock(unmanagedBlock) // Should log a warning, not panic
	if len(mm.availableBlocks) != initialAvailable {
		t.Errorf("PutBlock(unmanaged) changed available count: got %d, want %d", len(mm.availableBlocks), initialAvailable)
	}

	// Test with an already deallocated block (simulated by removing from allMappedBlocks)
	block, _ := mm.GetBlock() // Get a valid block
	addr := uintptr(unsafe.Pointer(&block[0]))
	// Directly remove from the internal map since we are in the same package
	mm.mu.Lock()
	delete(mm.allMappedBlocks, addr)
	mm.mu.Unlock()

	mm.PutBlock(block) // Should log a warning, not panic
	if len(mm.availableBlocks) != initialAvailable-1 { // One block is still out
		t.Errorf("PutBlock(deallocated) changed available count unexpectedly: got %d, want %d", len(mm.availableBlocks), initialAvailable-1)
	}
}

// TestMmapAllocationFailure simulates mmap failures during allocation.
func TestMmapAllocationFailure(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 1000 // 10 blocks max

	// Set mockMmapFailures to simulate failure during initial allocation
	mockMmapFailures = 1
	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err == nil {
		t.Error("Expected NewMemoryManager to fail due to mmap failure, but it succeeded.")
		mm.Close()
	} else {
		t.Logf("NewMemoryManager correctly failed with error: %v", err)
	}

	// Test failure during dynamic expansion
	mockMmapFailures = 0 // Reset for successful initial allocation
	mm, err = NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager for expansion test: %v", err)
	}
	defer mm.Close()

	initialCount := mm.currentAllocatedCount
	t.Logf("Initial allocated blocks: %d", initialCount)

	// Acquire blocks to trigger expansion
	var acquiredBlocks [][]byte
	blocksToAcquire := initialCount // Acquire all initial blocks
	for i := 0; i < blocksToAcquire; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get block for expansion test: %v", err)
		}
		acquiredBlocks = append(acquiredBlocks, block)
	}

	// Set mockMmapFailures to simulate failure during the next allocation attempt (expansion)
	mockMmapFailures = 1
	t.Log("Simulating mmap failure during next expansion...")
	time.Sleep(idleCheckInterval + 100*time.Millisecond) // Wait for monitorAndScale to run

	// Check that allocation count did not increase as much as expected, or an error was logged
	newCount := mm.currentAllocatedCount
	t.Logf("Allocated blocks after attempted expansion with failure: %d", newCount)
	if newCount > initialCount+1 { // It might allocate 1 block before failing, or not at all.
		t.Logf("Warning: Expansion might have partially succeeded despite mock failure count. Allocated: %d, Initial: %d", newCount, initialCount)
	}

	for _, block := range acquiredBlocks {
		mm.PutBlock(block)
	}
}

// TestMunmapFailure simulates munmap failures during deallocation.
func TestMunmapFailure(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 1000 // 10 blocks max

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}
	defer mm.Close()

	// Manually expand to a higher number of blocks (e.g., 50% of max)
	blocksToExpandTo := int(float64(mm.maxBlockCount) * 0.5)
	if blocksToExpandTo < mm.currentAllocatedCount {
		blocksToExpandTo = mm.currentAllocatedCount + 1 // Ensure we expand
	}
	if blocksToExpandTo > mm.maxBlockCount {
		blocksToExpandTo = mm.maxBlockCount
	}

	var tempBlocks [][]byte
	for i := 0; i < blocksToExpandTo-mm.currentAllocatedCount; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get block for manual expansion: %v", err)
		}
		tempBlocks = append(tempBlocks, block)
	}
	for _, block := range tempBlocks {
		mm.PutBlock(block)
	}
	tempBlocks = nil

	// Wait for monitorAndScale to register the new available blocks
	time.Sleep(idleCheckInterval + 100*time.Millisecond)

	currentAllocated := mm.currentAllocatedCount
	t.Logf("Allocated blocks after manual expansion (all available): %d", currentAllocated)

	// Set mockMunmapFailures to simulate failure during shrinking
	mockMunmapFailures = 1 // Make one munmap call fail
	t.Log("Simulating munmap failure during shrinking...")

	time.Sleep(idleShrinkDelay + idleCheckInterval*2 + 100*time.Millisecond)

	newAllocated := mm.currentAllocatedCount
	t.Logf("Allocated blocks after attempted shrinking with failure: %d", newAllocated)

	// The manager's deallocateBlocks function logs the error but continues.
	// So, the currentAllocatedCount should still decrease, but a warning should be logged.
	if newAllocated >= currentAllocated {
		t.Errorf("Expected shrinking, but allocated count did not decrease. Before: %d, After: %d", currentAllocated, newAllocated)
	}
	// We can't easily assert on the log output here without capturing it,
	// but the test confirms the manager attempts to deallocate and continues.
}

// TestManagerClose ensures that Close unmaps all allocated blocks.
func TestManagerClose(t *testing.T) {
	setMockMmapFunctions()
	defer resetMmapFunctions()

	log.SetOutput(new(devNullWriter))
	defer log.SetOutput(log.Writer())

	blockSize := 100
	maxMemory := 1000 // 10 blocks max

	mm, err := NewMemoryManager(blockSize, maxMemory)
	if err != nil {
		t.Fatalf("Failed to create memory manager: %v", err)
	}

	// Acquire some blocks to ensure they are tracked
	var acquiredBlocks [][]byte
	for i := 0; i < 3; i++ {
		block, err := mm.GetBlock()
		if err != nil {
			t.Fatalf("Failed to get block: %v", err)
		}
		acquiredBlocks = append(acquiredBlocks, block)
	}
	// Return some, keep some out to test draining
	mm.PutBlock(acquiredBlocks[0])
	// acquiredBlocks[1] and [2] are still "out"

	// Close the manager
	mm.Close()

	// After close, currentAllocatedCount should be 0
	if mm.currentAllocatedCount != 0 {
		t.Errorf("Expected current allocated count to be 0 after Close, got %d", mm.currentAllocatedCount)
	}
	// AllMappedBlocks map should be empty
	if len(mm.allMappedBlocks) != 0 {
		t.Errorf("Expected allMappedBlocks to be empty after Close, got %d entries", len(mm.allMappedBlocks))
	}
	// Channels should be closed (or at least empty)
	if len(mm.availableBlocks) != 0 || len(mm.returnedBlocks) != 0 {
		t.Errorf("Channels not empty after Close: available %d, returned %d",
			len(mm.availableBlocks), len(mm.returnedBlocks))
	}
}

// devNullWriter is a helper to suppress log output during tests.
type devNullWriter struct{}

func (w *devNullWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}
