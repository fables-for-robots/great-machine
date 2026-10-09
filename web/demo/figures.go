package main

import (
	"math/rand/v2"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// sampleEvery is how often a zathras has a new sample.
	sampleEvery = 2 * time.Second
	// history is how long samples are kept, and how far back they are
	// made up at the start.
	history = 15 * time.Minute

	kib = 1 << 10
	mib = 1 << 20
)

// figures makes up the samples of one container, each from the one
// before: a load that wanders, memory that creeps up and is collected,
// and a network that is quiet but for its bursts.
type figures struct {
	rng *rand.Rand

	load  float64 // cores the service uses when nothing happens
	limit uint64  // of memory, in bytes; 0 for none
	floor float64 // bytes the service holds after a collection
	ceil  float64 // bytes at which it collects

	cpu    float64
	memory float64
	burst  int     // samples the burst of the network still lasts
	rate   float64 // bytes per second of the burst
}

// newFigures returns the figures of a container. seed tells one container
// from another, and the same seed makes the same figures.
func newFigures(seed uint64, load float64, limit uint64) *figures {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	floor := (120 + 80*rng.Float64()) * mib
	return &figures{
		rng:    rng,
		load:   load,
		limit:  limit,
		floor:  floor,
		ceil:   floor * (1.8 + 0.6*rng.Float64()),
		cpu:    load,
		memory: floor * (1 + 0.5*rng.Float64()),
	}
}

// next returns the sample of the time at. A container whose service does
// not run has zathras alone in it.
func (f *figures) next(at time.Time, running bool) wire.Sample {
	if !running {
		f.cpu, f.memory, f.burst = 0.002, 14*mib, 0
		return wire.Sample{At: at, CPU: f.cpu, Memory: uint64(f.memory), MemoryLimit: f.limit}
	}
	if f.memory < f.floor/2 {
		f.memory = f.floor * 0.6 // just started
	}

	// The load is pulled back to where it belongs, and now and then
	// something asks a lot of it.
	f.cpu += 0.2*(f.load-f.cpu) + f.rng.NormFloat64()*f.load*0.12
	if f.rng.Float64() < 0.02 {
		f.cpu += f.load * (1 + 3*f.rng.Float64())
	}
	f.cpu = min(max(f.cpu, 0.003), 4)

	// Memory creeps up until the service collects.
	f.memory += (0.2 + 1.2*f.rng.Float64()) * mib
	if f.memory > f.ceil*(0.9+0.1*f.rng.Float64()) {
		f.memory = f.floor * (1 + 0.15*f.rng.Float64())
	}

	// The network: a murmur, and bursts of some seconds.
	if f.burst == 0 && f.rng.Float64() < 0.03 {
		f.burst = 3 + f.rng.IntN(12)
		f.rate = (1 + 7*f.rng.Float64()) * mib
	}
	rx := (20 + 40*f.rng.Float64()) * kib * (0.5 + f.cpu/f.load/2)
	if f.burst > 0 {
		f.burst--
		rx += f.rate * (0.7 + 0.6*f.rng.Float64())
	}

	// The disk: little is written all the time, and at times much is
	// read or written at once.
	read, write := 0.0, (4+30*f.rng.Float64())*kib
	if f.rng.Float64() < 0.04 {
		read = (0.5 + 20*f.rng.Float64()) * mib
	}
	if f.rng.Float64() < 0.03 {
		write += (1 + 6*f.rng.Float64()) * mib
	}

	return wire.Sample{
		At:          at,
		CPU:         f.cpu,
		Memory:      uint64(f.memory),
		MemoryLimit: f.limit,
		DiskRead:    read,
		DiskWrite:   write,
		NetRx:       rx,
		NetTx:       rx * (0.4 + 0.4*f.rng.Float64()),
	}
}
