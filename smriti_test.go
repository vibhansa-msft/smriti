package smriti

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type smritiTestSuite struct {
	suite.Suite
	assert         *assert.Assertions
	smritiInstance *Smriti
}

func (s *smritiTestSuite) SetupTest() {
	// Setup code if needed
	s.assert = assert.New(s.T())
}

func (s *smritiTestSuite) TearDownTest() {
	if s.smritiInstance != nil {
		s.smritiInstance.Close()
	}
	s.smritiInstance = nil
}

func (s *smritiTestSuite) TestInvalidConfig() {
	_, err := NewSmriti(0, 10)
	s.assert.NotNil(err)

	_, err = NewSmriti(1024, 0)
	s.assert.NotNil(err)

	_, err = NewSmriti(-1024, 10)
	s.assert.NotNil(err)

	_, err = NewSmriti(1024, -10)
	s.assert.NotNil(err)
}

func (s *smritiTestSuite) TestInitialAllocations() {
	var err error
	s.smritiInstance, err = NewSmriti(10, 1)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(1, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(1, s.smritiInstance.initialBlockCount)
	s.smritiInstance.Close()

	s.smritiInstance, err = NewSmriti(10, 20)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialBlockCount)

	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(4, avilable)
	s.assert.Equal(0, pending)
}

func (s *smritiTestSuite) TestExpansion() {
	var err error
	s.smritiInstance, err = NewSmriti(10, 10)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)

	var blocks map[int][]byte = make(map[int][]byte)
	for i := range 5 {
		block, err := s.smritiInstance.Allocate()
		s.assert.Nil(err)
		s.assert.NotNil(block)
		blocks[i] = block
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)
	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(0, avilable)
	s.assert.Equal(0, pending)

	for i := range 5 {
		err = s.smritiInstance.Free(blocks[i])
		s.assert.Nil(err)
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process
	allocated, avilable, pending = s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(5, avilable+pending)
}

func (s *smritiTestSuite) TestShrink() {
	var err error
	s.smritiInstance, err = NewSmriti(10, 10)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)

	var blocks map[int][]byte = make(map[int][]byte)
	for i := range 5 {
		block, err := s.smritiInstance.Allocate()
		s.assert.Nil(err)
		s.assert.NotNil(block)
		blocks[i] = block
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)
	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(0, avilable)
	s.assert.Equal(0, pending)

	for i := range 5 {
		err = s.smritiInstance.Free(blocks[i])
		s.assert.Nil(err)
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)

	time.Sleep((ShrinkTimeout + 10) * time.Second) // Give some time for the background goroutine to process
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
}

func (s *smritiTestSuite) TestAllocFailure() {
	var err error
	s.smritiInstance, err = NewSmriti(10, 10)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialBlockCount)

	var blocks map[int][]byte = make(map[int][]byte)
	for i := range 10 {
		block, err := s.smritiInstance.Allocate()
		s.assert.Nil(err)
		s.assert.NotNil(block)
		blocks[i] = block
	}

	block, err := s.smritiInstance.Allocate()
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "cannot expand further")
	s.assert.Nil(block)

	for i := range 10 {
		err = s.smritiInstance.Free(blocks[i])
		s.assert.Nil(err)
	}
}

func (s *smritiTestSuite) TestFakeFree() {
	var err error
	s.smritiInstance, err = NewSmriti(1, 2)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)

	block := make([]byte, 1)
	err = s.smritiInstance.Free(block)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "attempted to return an unknown block")

	block = make([]byte, 3)
	err = s.smritiInstance.Free(block)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size mismatch")
}

func (s *smritiTestSuite) TestStats() {
	var err error
	s.smritiInstance, err = NewSmriti(10, 20)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialBlockCount)

	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(4, avilable)
	s.assert.Equal(0, pending)

	block, err := s.smritiInstance.Allocate()
	s.assert.Nil(err)
	s.assert.NotNil(block)
	allocated, avilable, pending = s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(3, avilable)
	s.assert.Equal(0, pending)

	err = s.smritiInstance.Free(block)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process

	s.assert.Nil(err)
	allocated, avilable, pending = s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(4, avilable)
	s.assert.Equal(0, pending)
}

func TestSmriti(t *testing.T) {
	suite.Run(t, new(smritiTestSuite))
}
