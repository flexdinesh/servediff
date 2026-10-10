package httpapi

import "github.com/flexdinesh/diffx/internal/reviewservice"

type Store interface{ reviewservice.MutationStore }
