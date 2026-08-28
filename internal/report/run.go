package report

import (
	"context"
	"errors"
)

type TaskLabels struct {
	Run, Status, Done, Fail string
}

// RunTask runs a task with the given labels, reporting progress through the provided function.
// It handles live task status updates and error reporting.
func RunTask(rep Reporter, labels TaskLabels, fn func(progress func(done, total int64)) error) error {
	task := rep.Task(labels.Run)
	task.Status(labels.Status)
	err := fn(func(done, total int64) { task.Count(int(done), int(total)) })
	if err != nil {
		task.Fail(labels.Fail)
		return err
	}
	task.Done(labels.Done)
	return nil
}

// IsCancellation checks if the given error is a cancellation error (context.Canceled or context.DeadlineExceeded).
func IsCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
