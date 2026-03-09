package timeseries

import "math"

// Sample represents a single time-series data point.
type Sample struct {
	Value     float64
	Timestamp int64
}

// RingBuffer is a fixed-size sliding window of time-series samples.
type RingBuffer struct {
	samples  []Sample
	head     int
	count    int
	capacity int
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		samples:  make([]Sample, capacity),
		capacity: capacity,
	}
}

// Push adds a new sample to the ring buffer, overwriting the oldest if full.
func (rb *RingBuffer) Push(value float64, ts int64) {
	rb.samples[rb.head] = Sample{Value: value, Timestamp: ts}
	rb.head = (rb.head + 1) % rb.capacity
	if rb.count < rb.capacity {
		rb.count++
	}
}

// Samples returns a copy of all samples in oldest-first order.
func (rb *RingBuffer) Samples() []Sample {
	if rb.count == 0 {
		return nil
	}
	result := make([]Sample, rb.count)
	start := (rb.head - rb.count + rb.capacity) % rb.capacity
	for i := range rb.count {
		result[i] = rb.samples[(start+i)%rb.capacity]
	}
	return result
}

// Values returns just the float64 values in oldest-first order.
func (rb *RingBuffer) Values() []float64 {
	if rb.count == 0 {
		return nil
	}
	result := make([]float64, rb.count)
	start := (rb.head - rb.count + rb.capacity) % rb.capacity
	for i := range rb.count {
		result[i] = rb.samples[(start+i)%rb.capacity].Value
	}
	return result
}

// Last returns the most recent sample value, or 0 if empty.
func (rb *RingBuffer) Last() float64 {
	if rb.count == 0 {
		return 0
	}
	idx := (rb.head - 1 + rb.capacity) % rb.capacity
	return rb.samples[idx].Value
}

// Min returns the minimum value in the buffer.
func (rb *RingBuffer) Min() float64 {
	if rb.count == 0 {
		return 0
	}
	min := math.MaxFloat64
	start := (rb.head - rb.count + rb.capacity) % rb.capacity
	for i := range rb.count {
		v := rb.samples[(start+i)%rb.capacity].Value
		if v < min {
			min = v
		}
	}
	return min
}

// Max returns the maximum value in the buffer.
func (rb *RingBuffer) Max() float64 {
	if rb.count == 0 {
		return 0
	}
	max := -math.MaxFloat64
	start := (rb.head - rb.count + rb.capacity) % rb.capacity
	for i := range rb.count {
		v := rb.samples[(start+i)%rb.capacity].Value
		if v > max {
			max = v
		}
	}
	return max
}

// Avg returns the average value in the buffer.
func (rb *RingBuffer) Avg() float64 {
	if rb.count == 0 {
		return 0
	}
	var sum float64
	start := (rb.head - rb.count + rb.capacity) % rb.capacity
	for i := range rb.count {
		sum += rb.samples[(start+i)%rb.capacity].Value
	}
	return sum / float64(rb.count)
}

// Count returns the number of samples currently stored.
func (rb *RingBuffer) Count() int {
	return rb.count
}
