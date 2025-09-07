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
	_, err := New(0, 10)
	s.assert.NotNil(err)

	_, err = New(1024, 0)
	s.assert.NotNil(err)

	_, err = New(-1024, 10)
	s.assert.NotNil(err)

	_, err = New(1024, -10)
	s.assert.NotNil(err)
}

func (s *smritiTestSuite) TestInitialAllocations() {
	var err error
	s.smritiInstance, err = New(10, 1)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(1, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(1, s.smritiInstance.initialBlockCount)
	s.smritiInstance.Close()

	s.smritiInstance, err = New(10, 20)
	s.assert.Nil(err)
	s.assert.NotNil(s.smritiInstance)
	s.assert.Equal(4, s.smritiInstance.currentAllocatedCount)
	s.assert.Equal(4, s.smritiInstance.initialBlockCount)

	allocated, avilable, pending := s.smritiInstance.Stats()
	s.assert.Equal(4, allocated)
	s.assert.Equal(0, avilable)
	s.assert.Equal(0, pending)
}

func (s *smritiTestSuite) TestStats() {
	var err error
	s.smritiInstance, err = New(10, 20)
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
