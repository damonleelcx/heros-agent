package deliveryrecord

import "errors"

// errUnreachable is returned by a store taken offline. It exists so the delivery core's per-repository
// failure isolation (a store or forge outage does not lose a proposal) is exercisable.
var errUnreachable = errors.New("deliveryrecord: store unreachable")
