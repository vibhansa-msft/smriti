package smriti

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type smritiPatalTestSuite struct {
	suite.Suite
	assert              *assert.Assertions
	smritiPatalInstance *SmritiPatal
}

func (s *smritiPatalTestSuite) SetupTest() {
	// Setup code if needed
	s.assert = assert.New(s.T())
}

func (s *smritiPatalTestSuite) TearDownTest() {
	if s.smritiPatalInstance != nil {
		s.smritiPatalInstance.Close()
	}
	s.smritiPatalInstance = nil
}

func (s *smritiPatalTestSuite) TestInvalidConfig() {
	_, err := NewSmritiPatal([]SmritiConfig{})
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "at least one configuration is required")

	_, err = NewSmritiPatal([]SmritiConfig{{Size: 0, Count: 10}})
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size and block count must be non zero")

	_, err = NewSmritiPatal([]SmritiConfig{{Size: 1024, Count: 0}})
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size and block count must be non zero")

	_, err = NewSmritiPatal([]SmritiConfig{{Size: -1024, Count: 10}})
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size and block count must be non zero")

	_, err = NewSmritiPatal([]SmritiConfig{{Size: 1024, Count: -10}})
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size and block count must be non zero")
}
func (s *smritiPatalTestSuite) TestValidConfig() {
	var err error
	s.smritiPatalInstance, err = NewSmritiPatal([]SmritiConfig{
		{Size: 10, Count: 1},
		{Size: 20, Count: 2},
		{Size: 30, Count: 3},
	})
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiPatalInstance)
}

func (s *smritiPatalTestSuite) TestAllocation() {
	var err error
	s.smritiPatalInstance, err = NewSmritiPatal([]SmritiConfig{
		{Size: 10, Count: 1},
		{Size: 20, Count: 2},
		{Size: 30, Count: 3},
	})
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiPatalInstance)

	blk, err := s.smritiPatalInstance.Allocate(10)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(10, len(blk))

	blk, err = s.smritiPatalInstance.Allocate(20)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(20, len(blk))

	blk, err = s.smritiPatalInstance.Allocate(30)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(30, len(blk))
}

func (s *smritiPatalTestSuite) TestAllocationFailure() {
	var err error
	s.smritiPatalInstance, err = NewSmritiPatal([]SmritiConfig{
		{Size: 10, Count: 1},
		{Size: 20, Count: 2},
		{Size: 30, Count: 3},
	})
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiPatalInstance)

	blk, err := s.smritiPatalInstance.Allocate(10)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(10, len(blk))

	blk, err = s.smritiPatalInstance.Allocate(10)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "no blocks available and cannot expand further")
	s.assert.Nil(blk)

	blk, err = s.smritiPatalInstance.Allocate(25)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "no Smriti instance for block size")
	s.assert.Nil(blk)
}

func (s *smritiPatalTestSuite) TestAllocationUpgrade() {
	var err error
	s.smritiPatalInstance, err = NewSmritiPatal([]SmritiConfig{
		{Size: 10, Count: 1},
		{Size: 20, Count: 2},
		{Size: 30, Count: 3},
	})
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiPatalInstance)

	blk, err := s.smritiPatalInstance.AllocateWithUpgrade(10)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(10, len(blk))

	blk, err = s.smritiPatalInstance.AllocateWithUpgrade(10)
	s.assert.Nil(err)
	s.assert.NotNil(blk)
	s.assert.Equal(20, len(blk))
}

func TestSmritiPatal(t *testing.T) {
	suite.Run(t, new(smritiPatalTestSuite))
}
