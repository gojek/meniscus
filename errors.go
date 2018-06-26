package meniscus

import "errors"

//ErrNoRequests ...
var ErrNoRequests = errors.New("no requests provided")

//ErrRequestIgnored ...
var ErrRequestIgnored = errors.New("request ignored")
