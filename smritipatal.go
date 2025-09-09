package smriti

import "fmt"

// SmritiConfig defines the configuration for a Smriti instance.
type SmritiConfig struct {
	Size  int // Size of each block in bytes (or any unit)
	Count int // Number of blocks of this size
}

// SmritiPatal manages multiple Smriti instances for different block sizes.
type SmritiPatal struct {
	configs []SmritiConfig  // Slice of configurations
	smritis map[int]*Smriti // Map of block size to Smriti instances)
}

func NewSmritiPatal(configs []SmritiConfig) (*SmritiPatal, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("at least one configuration is required")
	}

	// Based on the config, create smirit instances for each size
	sp := &SmritiPatal{
		configs: configs,
		smritis: make(map[int]*Smriti),
	}

	// Create Smriti instances for each configuration
	for _, cfg := range configs {
		if cfg.Size <= 0 || cfg.Count <= 0 {
			return nil, fmt.Errorf("block size and block count must be non zero")
		}

		sm, err := NewSmriti(cfg.Size, cfg.Count)
		if err != nil {
			return nil, err
		}

		// Store the Smriti instance in the map
		sp.smritis[cfg.Size] = sm
	}

	return sp, nil
}

func (sp *SmritiPatal) Close() {
	// Close all Smriti instances
	for _, sm := range sp.smritis {
		sm.Close()
	}
}

// Allocate allocates a block of the specified size from the appropriate Smriti instance.
func (sp *SmritiPatal) Allocate(blockSize int) ([]byte, error) {
	return sp.allocateInternal(blockSize)
}

// AllocateWithUpgrade tries to allocate a block of the specified size.
func (sp *SmritiPatal) AllocateWithUpgrade(blockSize int) ([]byte, error) {
	blk, err := sp.allocateInternal(blockSize)
	if err == nil {
		return blk, nil
	}

	// If allocation failed and upgrade is allowed, try larger sizes
	for size, _ := range sp.smritis {
		if size > blockSize {
			return sp.AllocateWithUpgrade(size)
		}
	}

	return nil, fmt.Errorf("no suitable block found for size %d", blockSize)
}

func (sp *SmritiPatal) allocateInternal(blockSize int) ([]byte, error) {
	sm, exists := sp.smritis[blockSize]
	if !exists {
		// If nothing is possible then return error
		return nil, fmt.Errorf("no Smriti instance for block size %d", blockSize)
	}

	return sm.Allocate()
}

// Free returns a block to the appropriate Smriti instance based on its size.
func (sp *SmritiPatal) Free(block []byte) error {
	if len(block) == 0 {
		return fmt.Errorf("cannot free an empty block")
	}

	blockSize := cap(block)
	sm, exists := sp.smritis[blockSize]
	if !exists {
		return fmt.Errorf("no Smriti instance for block size %d", blockSize)
	}

	return sm.Free(block)
}
