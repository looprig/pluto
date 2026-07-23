package mpqttest_test

import (
	"github.com/looprig/mpqt/pkg/profile"
	"github.com/looprig/mpqt/pkg/qual"
)

// var _ profile.Card = qual.Scorecard{} is a compile-time proof that
// qual.Scorecard satisfies profile.Card once FindingCount/SeverityCount are
// added. This file imports both mpqt and profile alongside mpqttest, which is
// where the two packages are naturally used together.
var _ profile.Card = qual.Scorecard{}
