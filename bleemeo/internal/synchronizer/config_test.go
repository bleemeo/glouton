package synchronizer

import (
	"sync"
	"testing"
)

// TestParallelSync should be run with the -race flag
func TestParallelSync(t *testing.T) {
	helper := newHelper(t)
	defer helper.Close()

	helper.preregisterAgent(t)
	helper.initSynchronizer(t)

	wg := new(sync.WaitGroup)

	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := helper.runOnce(t)
			if err != nil {
				t.Error(err)
				return
			}
		}()
	}

	wg.Wait()
}
