package tasks

import (
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
)

var _ taskv1.TaskServiceServer = (*Service)(nil)
var _ dispatcherv1.DispatcherServiceServer = (*Dispatcher)(nil)
var _ executorv1.ExecutorServiceServer = (*Dispatcher)(nil)
