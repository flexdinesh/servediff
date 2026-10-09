package httpapi

import "github.com/flexdinesh/servediff/internal/reviewservice"

type Store interface{ reviewservice.MutationStore }
