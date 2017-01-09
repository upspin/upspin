package metric

import (
	"sync/atomic"

	"upspin.io/log"
)

func NewLogSaver() Saver {
	return &logSaver{}
}

type logSaver struct {
	processed int32
}

func (s *logSaver) Register(queue chan *Metric) {
	go func() {
		for metric := range queue {
			log.Debug.Println(metric)
			atomic.AddInt32(&s.processed, 1)
		}
	}()
}
func (s *logSaver) NumProcessed() int32 {
	return atomic.LoadInt32(&s.processed)
}
