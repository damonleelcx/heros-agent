package billing

// testledgerread_test.go holds the two helpers the tests read the ledger through.
//
// Ledger.Events and Ledger.Pending return an error now — they have to, because a durable ledger can
// fail and adminops sums an invoice total straight from Events, so a swallowed read error renders as
// "this customer owes nothing". See the interface comment in ledger.go.
//
// 🔴 The error is dropped HERE, in one named place, and nowhere else. MemLedger cannot fail, so an
// `if err != nil` at each of thirty assertion sites would be thirty branches that can never be taken —
// noise that trains a reader to skim exactly the check that matters in production. A panic keeps the
// impossible case loud if a future test ever substitutes a fallible ledger.

func testEvents(l Ledger, customerID, period string) []BillingEvent {
	rows, err := l.Events(customerID, period)
	if err != nil {
		panic("test ledger Events failed: " + err.Error())
	}
	return rows
}

func testPending(l Ledger) []BillingEvent {
	rows, err := l.Pending()
	if err != nil {
		panic("test ledger Pending failed: " + err.Error())
	}
	return rows
}
