package plutotest_test

import (
	"github.com/looprig/pluto/pkg/profile"
	"github.com/looprig/pluto/pkg/qual"
)

// var _ profile.Card = qual.Scorecard{} is a compile-time proof that
// qual.Scorecard satisfies profile.Card once FindingCount/SeverityCount are
// added. This file imports both qual and profile alongside plutotest, which is
// where the two packages are naturally used together.
var _ profile.Card = qual.Scorecard{}
