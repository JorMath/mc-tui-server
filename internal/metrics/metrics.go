// Package metrics muestrea el uso de CPU y RAM de los procesos de los
// servidores (R5) usando gopsutil.
package metrics

import (
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Sample es una medición puntual de un proceso.
type Sample struct {
	CPUPercent float64
	RSSBytes   uint64
}

// proc abstrae process.Process para poder simular errores en tests.
type proc interface {
	Percent(interval time.Duration) (float64, error)
	MemoryInfo() (*process.MemoryInfoStat, error)
}

// newProc es inyectable en tests.
var newProc = func(pid int32) (proc, error) { return process.NewProcess(pid) }

// Collector cachea los handles de proceso entre muestras: gopsutil calcula
// el CPU% como delta desde la llamada anterior sobre el mismo handle, así
// que reutilizarlo es lo que hace la métrica significativa.
type Collector struct {
	mu    sync.Mutex
	procs map[int32]proc
}

// NewCollector crea un Collector vacío.
func NewCollector() *Collector {
	return &Collector{procs: map[int32]proc{}}
}

// Sample mide CPU% y memoria residente del PID dado. Si el proceso falla
// (por ejemplo, ya murió) se descarta de la caché y se devuelve el error.
func (c *Collector) Sample(pid int) (Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	p, ok := c.procs[int32(pid)]
	if !ok {
		var err error
		p, err = newProc(int32(pid))
		if err != nil {
			return Sample{}, fmt.Errorf("abriendo proceso %d: %w", pid, err)
		}
		c.procs[int32(pid)] = p
	}

	cpu, err := p.Percent(0)
	if err != nil {
		delete(c.procs, int32(pid))
		return Sample{}, fmt.Errorf("midiendo CPU del proceso %d: %w", pid, err)
	}
	mem, err := p.MemoryInfo()
	if err != nil {
		delete(c.procs, int32(pid))
		return Sample{}, fmt.Errorf("midiendo memoria del proceso %d: %w", pid, err)
	}
	return Sample{CPUPercent: cpu, RSSBytes: mem.RSS}, nil
}

// Forget elimina un PID de la caché (p.ej. cuando la instancia se detiene).
func (c *Collector) Forget(pid int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.procs, int32(pid))
}
