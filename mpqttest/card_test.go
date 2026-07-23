package mpqttest_test

import (
	"github.com/looprig/mpqt"
	"github.com/looprig/mpqt/profile"
)

// var _ profile.Card = mpqt.Scorecard{} is a compile-time proof that
// mpqt.Scorecard satisfies profile.Card once FindingCount/SeverityCount are
// added. This file imports both mpqt and profile alongside mpqttest, which is
// where the two packages are naturally used together.
var _ profile.Card = mpqt.Scorecard{}
