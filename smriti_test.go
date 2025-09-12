package smriti

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type testSt struct{}
type smritiTestSuite struct {
	suite.Suite
	assert         *assert.Assertions
	smritiInstance *Smriti[testSt]
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
	_, err := NewSmriti[testSt](0, 10, 0)
	s.assert.NotNil(err)

	_, err = NewSmriti[testSt](1024, 0, 0)
	s.assert.NotNil(err)

	_, err = NewSmriti[testSt](-1024, 10, 0)
	s.assert.NotNil(err)

	_, err = NewSmriti[testSt](1024, -10, 0)
	s.assert.NotNil(err)
}

func (s *smritiTestSuite) TestInitialAllocations() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](10, 1, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(1, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(1, s.smritiInstance.initialCount)
	s.smritiInstance.Close()

	s.smritiInstance, err = NewSmriti[testSt](10, 20, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialCount)

	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(4, avilable)
	s.assert.Equal(0, pending)
}

func (s *smritiTestSuite) TestExpansion() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](10, 10, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)

	var blocks map[int]*Sanrachna[testSt] = make(map[int]*Sanrachna[testSt])
	for i := range 5 {
		block, err := s.smritiInstance.Allocate()
		s.assert.Nil(err)
		s.assert.NotNil(block)
		blocks[i] = block
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)
	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(0, avilable)
	s.assert.Equal(0, pending)

	for i := range 5 {
		err = s.smritiInstance.Free(blocks[i])
		s.assert.Nil(err)
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process
	allocated, avilable, pending = s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(5, avilable+pending)
}

func (s *smritiTestSuite) TestShrink() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](10, 10, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)

	var blocks map[int]*Sanrachna[testSt] = make(map[int]*Sanrachna[testSt])
	for i := range 5 {
		block, err := s.smritiInstance.Allocate()
		s.assert.Nil(err)
		s.assert.NotNil(block)
		blocks[i] = block
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)
	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(5, allocated)
	s.assert.Equal(0, avilable)
	s.assert.Equal(0, pending)

	for i := range 5 {
		err = s.smritiInstance.Free(blocks[i])
		s.assert.Nil(err)
	}

	s.assert.Equal(5, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)

	time.Sleep((ShrinkTimeout + 10) * time.Second) // Give some time for the background goroutine to process
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
}

func (s *smritiTestSuite) TestAllocFailure() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](10, 10, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(2, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(2, s.smritiInstance.initialCount)

	var blocks map[int]*Sanrachna[testSt] = make(map[int]*Sanrachna[testSt])
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
	s.smritiInstance, err = NewSmriti[testSt](1, 2, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)

	err = s.smritiInstance.Free(nil)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "cannot return a nil block")

	block := &Sanrachna[testSt]{bytes: nil}
	err = s.smritiInstance.Free(block)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size mismatch")

	block = &Sanrachna[testSt]{}
	block.bytes = make([]byte, 1)
	err = s.smritiInstance.Free(block)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "attempted to return an unknown block")

	block = &Sanrachna[testSt]{}
	block.bytes = make([]byte, 3)
	err = s.smritiInstance.Free(block)
	s.assert.NotNil(err)
	s.assert.Contains(err.Error(), "block size mismatch")
}

func (s *smritiTestSuite) TestStats() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](10, 20, 0)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialCount)

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

func (s *smritiTestSuite) TestSmritiReservation() {
	var err error
	s.smritiInstance, err = NewSmriti[testSt](1, 10, 20)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialCount)
	s.assert.Equal(2, s.smritiInstance.reservedCount)

	b, err := s.smritiInstance.AllocateReserved()
	s.assert.Nil(err)
	s.assert.NotNil(b)
	s.assert.Equal(2, len(s.smritiInstance.available))
	s.assert.Equal(1, len(s.smritiInstance.reserved))

	b2, err := s.smritiInstance.AllocateReserved()
	s.assert.Nil(err)
	s.assert.NotNil(b2)
	s.assert.Equal(2, len(s.smritiInstance.available))
	s.assert.Equal(0, len(s.smritiInstance.reserved))

	// Next reserved allocation should fallback to regular allocation
	b3, err := s.smritiInstance.AllocateReserved()
	s.assert.Nil(err)
	s.assert.NotNil(b3)
	s.assert.Equal(1, len(s.smritiInstance.available))
	s.assert.Equal(0, len(s.smritiInstance.reserved))

	err = s.smritiInstance.Free(b)
	s.assert.Nil(err)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process
	s.assert.Equal(1, len(s.smritiInstance.available))
	s.assert.Equal(1, len(s.smritiInstance.reserved))

	err = s.smritiInstance.Free(b2)
	s.assert.Nil(err)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process
	s.assert.Equal(1, len(s.smritiInstance.available))
	s.assert.Equal(2, len(s.smritiInstance.reserved))

	err = s.smritiInstance.Free(b3)
	s.assert.Nil(err)
	time.Sleep(1 * time.Second) // Give some time for the background goroutine to process
	s.assert.Equal(2, len(s.smritiInstance.available))
	s.assert.Equal(2, len(s.smritiInstance.reserved))
}

func TestSmriti(t *testing.T) {
	suite.Run(t, new(smritiTestSuite))
}
