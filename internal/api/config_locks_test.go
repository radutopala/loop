package api

import (
	"net/http/httptest"
	"time"
)

// requestUnderConfigLock holds path's config lock while fire runs in the
// background, checks the request is still waiting 50ms later and runs
// whileHeld to check nothing was read or written yet, then unlocks and
// returns the finished response.
func (s *ServerSuite) requestUnderConfigLock(path string, fire func() *httptest.ResponseRecorder, whileHeld func()) *httptest.ResponseRecorder {
	unlock := s.srv.configLocks.lock(path)
	done := make(chan *httptest.ResponseRecorder)
	go func() {
		done <- fire()
	}()

	select {
	case <-done:
		unlock()
		s.T().Fatal("the request ran while another edit held the config lock")
	case <-time.After(50 * time.Millisecond):
	}
	whileHeld()

	unlock()
	return <-done
}
