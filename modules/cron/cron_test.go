package cron

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Many tests schedule a job for every second, and then wait at most a second
// for it to run.  This amount is just slightly larger than 1 second to
// compensate for a few milliseconds of runtime.
const ONE_SECOND = 1*time.Second + 10*time.Millisecond

// Start and stop cron with no entries.
func TestNoEntries(t *testing.T) {
	cron := New()
	cron.Start()

	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-stop(cron):
	}
}

// Start, stop, then add an entry. Verify entry doesn't run.
func TestStopCausesJobsToNotRun(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	cron := New()
	cron.Start()
	cron.Stop()
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })

	select {
	case <-time.After(ONE_SECOND):
		// No job ran!
	case <-wait(wg):
		t.FailNow()
	}
}

// Add a job, start cron, expect it runs.
func TestAddBeforeRunning(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	cron := New()
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })
	cron.Start()
	defer cron.Stop()

	// Give cron 2 seconds to run our job (which is always activated).
	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

// Start cron, add a job, expect it runs.
func TestAddWhileRunning(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	cron := New()
	cron.Start()
	defer cron.Stop()
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })

	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

// Test timing with Entries.
func TestSnapshotEntries(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	cron := New()
	cron.AddFunc("", "@every 2s", func() { wg.Done() })
	cron.Start()
	defer cron.Stop()

	// Cron should fire in 2 seconds. After 1 second, call Entries.
	select {
	case <-time.After(ONE_SECOND):
		cron.Entries()
	}

	// Even though Entries was called, the cron should fire at the 2 second mark.
	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}

}

// Test that the entries are correctly sorted.
// Add a bunch of long-in-the-future entries, and an immediate entry, and ensure
// that the immediate entry runs immediately.
// Also: Test that multiple jobs run in the same instant.
func TestMultipleEntries(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	cron := New()
	cron.AddFunc("", "0 0 0 1 1 ?", func() {})
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })
	cron.AddFunc("", "0 0 0 31 12 ?", func() {})
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })

	cron.Start()
	defer cron.Stop()

	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

// Test running the same job twice.
func TestRunningJobTwice(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	cron := New()
	cron.AddFunc("", "0 0 0 1 1 ?", func() {})
	cron.AddFunc("", "0 0 0 31 12 ?", func() {})
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })

	cron.Start()
	defer cron.Stop()

	select {
	case <-time.After(2 * ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

func TestRunningMultipleSchedules(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(2)

	cron := New()
	cron.AddFunc("", "0 0 0 1 1 ?", func() {})
	cron.AddFunc("", "0 0 0 31 12 ?", func() {})
	cron.AddFunc("", "* * * * * ?", func() { wg.Done() })
	cron.Schedule("", "", Every(time.Minute), FuncJob(func() {}))
	cron.Schedule("", "", Every(time.Second), FuncJob(func() { wg.Done() }))
	cron.Schedule("", "", Every(time.Hour), FuncJob(func() {}))

	cron.Start()
	defer cron.Stop()

	select {
	case <-time.After(2 * ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

// Test that the cron is run in the local time zone (as opposed to UTC).
func TestLocalTimezone(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	now := time.Now().Local()
	spec := fmt.Sprintf("%d %d %d %d %d ?",
		now.Second()+1, now.Minute(), now.Hour(), now.Day(), now.Month())

	cron := New()
	cron.AddFunc("", spec, func() { wg.Done() })
	cron.Start()
	defer cron.Stop()

	select {
	case <-time.After(ONE_SECOND):
		t.FailNow()
	case <-wait(wg):
	}
}

type testJob struct {
	wg   *sync.WaitGroup
	name string
}

func (t testJob) Run() {
	t.wg.Done()
}

type observedTestJob struct {
	name      string
	completed chan<- time.Time
}

func (j observedTestJob) Run() {
	if j.name == "job2" {
		select {
		case j.completed <- time.Now():
		default:
		}
	}
}

type startupGatedSchedule struct {
	Schedule
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (s *startupGatedSchedule) Next(now time.Time) time.Time {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.Schedule.Next(now)
}

type firstDueSchedule struct {
	Schedule
	first chan<- time.Time
	once  sync.Once
}

func (s *firstDueSchedule) Next(now time.Time) time.Time {
	// Record the first due time before the scheduler can advance Entry.Next.
	next := s.Schedule.Next(now)
	s.once.Do(func() { s.first <- next })
	return next
}

// Exercise actual asynchronous Runnable dispatch and schedule ordering.
func TestJob(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayed-initialization=%v", delayed), func(t *testing.T) {
			completed := make(chan time.Time, 8)
			cron := New()
			cron.AddJob("", "0 0 0 30 Feb ?", observedTestJob{"job0", completed})
			cron.AddJob("", "0 0 0 1 1 ?", observedTestJob{"job1", completed})
			cron.AddJob("", "* * * * * ?", observedTestJob{"job2", completed})
			cron.AddJob("", "1 0 0 1 1 ?", observedTestJob{"job3", completed})
			cron.Schedule("", "", Every(5*time.Second+5*time.Nanosecond), observedTestJob{"job4", completed})
			cron.Schedule("", "", Every(5*time.Minute), observedTestJob{"job5", completed})
			firstDue := make(chan time.Time, 1)
			cron.entries[2].Schedule = &firstDueSchedule{Schedule: cron.entries[2].Schedule, first: firstDue}
			release := make(chan struct{})
			entered := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			if delayed {
				cron.entries[0].Schedule = &startupGatedSchedule{Schedule: cron.entries[0].Schedule, entered: entered, release: release}
			}
			started := time.Now()
			cron.Start()
			defer func() { unblock(); cron.Stop() }()
			if delayed {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("scheduler did not reach initialization latch")
				}
				// Deliberately spend the old whole-test budget in initialization.
				// This is a controlled counterexample, not attribution of a past delay.
				select {
				case <-completed:
					t.Fatal("job ran before its schedule was initialized")
				case <-time.After(ONE_SECOND + 50*time.Millisecond):
				}
				unblock()
			}
			var actual time.Time
			if delayed {
				// Force the first callback to precede the snapshot observation.
				select {
				case actual = <-completed:
				case <-time.After(5 * time.Second):
					t.Fatal("first callback did not reach observation latch")
				}
			}
			var scheduled time.Time
			select {
			case scheduled = <-firstDue:
			case <-time.After(5 * time.Second):
				t.Fatal("first due time was not recorded")
			}
			ready := cron.Entries()
			if ready[0].Job.(observedTestJob).name != "job2" || scheduled.IsZero() {
				t.Fatal("first runnable schedule was not initialized")
			}
			if delayed && !ready[0].Next.After(scheduled) {
				t.Fatal("observation did not follow first callback advancement")
			}
			// Wait for the actual callback, bounded relative to its scheduled time.
			// The scheduler has no 10ms goroutine-start/dispatch latency guarantee.
			if actual.IsZero() {
				select {
				case actual = <-completed:
				default:
				}
			}
			if actual.IsZero() {
				deadline := time.Until(scheduled) + 5*time.Second
				if deadline <= 0 {
					t.Fatal("initial schedule already exceeded dispatch grace")
				}
				select {
				case <-time.After(deadline):
					t.Fatalf("job did not complete: started=%s scheduled=%s now=%s", started.Format(time.RFC3339Nano), scheduled.Format(time.RFC3339Nano), time.Now().Format(time.RFC3339Nano))
				case actual = <-completed:
				}
			}
			if actual.Before(scheduled) || actual.After(scheduled.Add(5*time.Second)) {
				t.Fatalf("callback=%s outside first-due=%s dispatch window", actual.Format(time.RFC3339Nano), scheduled.Format(time.RFC3339Nano))
			}
			t.Logf("startup=%s first-due=%s snapshot-next=%s actual=%s dispatch-lateness=%s", started.Format(time.RFC3339Nano), scheduled.Format(time.RFC3339Nano), ready[0].Next.Format(time.RFC3339Nano), actual.Format(time.RFC3339Nano), actual.Sub(scheduled))
			expecteds := []string{"job2", "job4", "job5", "job1", "job3", "job0"}
			for i, entry := range cron.Entries() {
				if name := entry.Job.(observedTestJob).name; name != expecteds[i] {
					t.Fatalf("entry %d = %s want %s", i, name, expecteds[i])
				}
				if i == 0 && (entry.ExecTimes < 1 || entry.Prev.Before(scheduled) || entry.Next.Sub(entry.Prev) != time.Second) {
					t.Fatalf("incorrect recurring schedule state: %+v", entry)
				}
			}
		})
	}
}

func wait(wg *sync.WaitGroup) chan bool {
	ch := make(chan bool)
	go func() {
		wg.Wait()
		ch <- true
	}()
	return ch
}

func stop(cron *Cron) chan bool {
	ch := make(chan bool)
	go func() {
		cron.Stop()
		ch <- true
	}()
	return ch
}
