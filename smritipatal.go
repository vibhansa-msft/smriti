package smriti

import "fmt"

// SmritiConfig defines the configuration for a Smriti instance.
type SmritiConfig struct {
	Size           int // Size of each block in bytes (or any unit)
	Count          int // Number of blocks of this size
	ReservePercent int // Percentage of blocks to reserve for high-priority allocations
}

// SmritiPatal manages multiple Smriti instances for different block sizes.
type SmritiPatal[T any] struct {
	configs []SmritiConfig     // Slice of configurations
	smritis map[int]*Smriti[T] // Map of block size to Smriti instances)
}

func NewSmritiPatal[T any](configs []SmritiConfig) (*SmritiPatal[T], error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("at least one configuration is required")
	}

	// Based on the config, create smirit instances for each size
	sp := &SmritiPatal[T]{
		configs: configs,
		smritis: make(map[int]*Smriti[T]),
	}

	// Create Smriti instances for each configuration
	for _, cfg := range configs {
		if cfg.Size <= 0 || cfg.Count <= 0 {
			return nil, fmt.Errorf("block size and block count must be non zero")
		}

		sm, err := NewSmriti[T](cfg.Size, cfg.Count, cfg.ReservePercent)
		if err != nil {
			return nil, err
		}

		// Store the Smriti instance in the map
		sp.smritis[cfg.Size] = sm
	}

	return sp, nil
}

func (sp *SmritiPatal[T]) Close() {
	// Close all Smriti instances
	for _, sm := range sp.smritis {
		sm.Close()
	}
}

// Allocate allocates a block of the specified size from the appropriate Smriti instance.
func (sp *SmritiPatal[T]) Allocate(blockSize int) (*Sanrachna[T], error) {
	return sp.allocate(blockSize)
}

// AllocateWithUpgrade tries to allocate a block of the specified size.
func (sp *SmritiPatal[T]) AllocateWithUpgrade(blockSize int) (*Sanrachna[T], error) {
	blk, err := sp.allocate(blockSize)
	if err == nil {
		return blk, nil
	}

	// If allocation failed and upgrade is allowed, try larger sizes
	for size := range sp.smritis {
		if size > blockSize {
			return sp.AllocateWithUpgrade(size)
		}
	}

	return nil, fmt.Errorf("no suitable block found for size %d", blockSize)
}

func (sp *SmritiPatal[T]) allocate(blockSize int) (*Sanrachna[T], error) {
	sm, exists := sp.smritis[blockSize]
	if !exists {
		// If nothing is possible then return error
		return nil, fmt.Errorf("no Smriti instance for block size %d", blockSize)
	}

	return sm.Allocate()
}

// Free returns a block to the appropriate Smriti instance based on its size.
func (sp *SmritiPatal[T]) Free(block *Sanrachna[T]) error {
	if block == nil {
		return fmt.Errorf("cannot free a nil block")
	}

	if len(block.bytes) == 0 {
		return fmt.Errorf("cannot free an empty block")
	}

	blockSize := cap(block.bytes)
	sm, exists := sp.smritis[blockSize]
	if !exists {
		return fmt.Errorf("no Smriti instance for block size %d", blockSize)
	}

	return sm.Free(block)
}
