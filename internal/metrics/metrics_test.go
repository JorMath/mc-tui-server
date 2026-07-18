package metrics

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func TestSampleOwnProcess(t *testing.T) {
	c := NewCollector()
	// Dos muestras: la primera calibra el CPU%, la segunda ya usa el
	// proceso cacheado.
	if _, err := c.Sample(os.Getpid()); err != nil {
		t.Fatalf("Sample inicial: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	s, err := c.Sample(os.Getpid())
	if err != nil {
		t.Fatalf("Sample cacheado: %v", err)
	}
	if s.RSSBytes == 0 {
		t.Fatal("RSSBytes = 0, el proceso propio siempre usa memoria")
	}
	if s.CPUPercent < 0 {
		t.Fatalf("CPUPercent = %f, no puede ser negativo", s.CPUPercent)
	}
}

func TestSampleInvalidPIDFails(t *testing.T) {
	c := NewCollector()
	if _, err := c.Sample(-999); err == nil {
		t.Fatal("Sample con PID inválido debe fallar")
	}
}

func TestForgetDropsCache(t *testing.T) {
	c := NewCollector()
	if _, err := c.Sample(os.Getpid()); err != nil {
		t.Fatalf("Sample: %v", err)
	}
	c.Forget(os.Getpid())
	if _, ok := c.procs[int32(os.Getpid())]; ok {
		t.Fatal("el proceso sigue cacheado tras Forget")
	}
}

type fakeProc struct {
	cpuErr error
	memErr error
}

func (f fakeProc) Percent(time.Duration) (float64, error) { return 1.5, f.cpuErr }
func (f fakeProc) MemoryInfo() (*process.MemoryInfoStat, error) {
	return &process.MemoryInfoStat{RSS: 42}, f.memErr
}

func withFakeProc(t *testing.T, p proc) {
	t.Helper()
	orig := newProc
	newProc = func(pid int32) (proc, error) { return p, nil }
	t.Cleanup(func() { newProc = orig })
}

func TestSampleCPUErrorPropagates(t *testing.T) {
	withFakeProc(t, fakeProc{cpuErr: errors.New("cpu roto")})
	c := NewCollector()
	if _, err := c.Sample(1234); err == nil {
		t.Fatal("Sample debe propagar el error de CPU")
	}
	if _, ok := c.procs[1234]; ok {
		t.Fatal("un proceso que falla debe salir de la caché")
	}
}

func TestSampleMemoryErrorPropagates(t *testing.T) {
	withFakeProc(t, fakeProc{memErr: errors.New("mem rota")})
	c := NewCollector()
	if _, err := c.Sample(1234); err == nil {
		t.Fatal("Sample debe propagar el error de memoria")
	}
}

func TestSampleFakeValues(t *testing.T) {
	withFakeProc(t, fakeProc{})
	c := NewCollector()
	s, err := c.Sample(1234)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if s.CPUPercent != 1.5 || s.RSSBytes != 42 {
		t.Fatalf("Sample = %+v, quiero CPU 1.5 y RSS 42", s)
	}
}
