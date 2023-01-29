package delivery

import "time"

type ResourceHttpHandler struct {
	timeout time.Duration
}

func NewResourceHttpHandler(timeout time.Duration) *ResourceHttpHandler {
	return &ResourceHttpHandler{
		timeout: timeout,
	}
}
