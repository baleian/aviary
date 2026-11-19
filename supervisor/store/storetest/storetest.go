// Package storetest tests a store of the Supervisor's records: what holds of
// every store, whatever plugin connects to its database. The tests of a
// plugin run it against databases of the plugin's kind.
package storetest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"aviary/supervisor/store"
)

// Database is the database of one test, which begins with no records.
type Database interface {
	// Open returns a store on the database, as one more Supervisor has it.
	// The store is closed when the test ends.
	Open(t *testing.T) store.Store
	// Age makes the turns so much older: as if all that the database has
	// recorded of when something happened to them, and to their requests for
	// approval, had happened that long before it did.
	Age(t *testing.T, by time.Duration, turns ...uuid.UUID)
}

// keeps tells whether what a store has kept is the JSON that it was given.
// It need not be the same text, and the character U+FFFD may be in the place
// of the character U+0000, which a database may be unable to keep.
func keeps(t *testing.T, kept json.RawMessage, given string) bool {
	t.Helper()
	read := func(text string) any {
		var value any
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		return value
	}
	has, was := read(string(kept)), read(given)
	return reflect.DeepEqual(has, was) || reflect.DeepEqual(has, withoutNull(was))
}

// withoutNull returns the JSON value with U+FFFD in the place of every
// character U+0000.
func withoutNull(value any) any {
	switch value := value.(type) {
	case string:
		return strings.ReplaceAll(value, "\x00", "\ufffd")
	case []any:
		without := make([]any, len(value))
		for i, element := range value {
			without[i] = withoutNull(element)
		}
		return without
	case map[string]any:
		without := map[string]any{}
		for name, member := range value {
			without[withoutNull(name).(string)] = withoutNull(member)
		}
		return without
	}
	return value
}

// keepsAll tells whether a store has kept each of the JSON texts that it was
// given, in their order.
func keepsAll(t *testing.T, kept []json.RawMessage, given ...string) bool {
	t.Helper()
	if len(kept) != len(given) {
		return false
	}
	for i := range given {
		if !keeps(t, kept[i], given[i]) {
			return false
		}
	}
	return true
}

// suite is the tests of the stores of one kind.
type suite struct {
	// create makes a database for a test.
	create func(t *testing.T) Database
}

// Run tests the stores on the databases that create makes. Every test has a
// database of its own, since it counts the turns that it holds.
func Run(t *testing.T, create func(t *testing.T) Database) {
	suite := suite{create: create}
	for name, test := range map[string]func(*testing.T){
		"Members":                          suite.testMembers,
		"MembersWhoseUsernamesChange":      suite.testMembersWhoseUsernamesChange,
		"Permissions":                      suite.testPermissions,
		"Projects":                         suite.testProjects,
		"Roles":                            suite.testRoles,
		"SessionOfAUser":                   suite.testSessionOfAUser,
		"Users":                            suite.testUsers,
		"SupervisorsSeeAUserFirstTogether": suite.testSupervisorsSeeAUserFirstTogether,
		"Agents":                           suite.testAgents,
		"AgentsOfAProject":                 suite.testAgentsOfAProject,
		"SessionsOfAnAgent":                suite.testSessionsOfAnAgent,
		"SandboxOfASession":                suite.testSandboxOfASession,
		"EndSession":                       suite.testEndSession,
		"Providers":                        suite.testProviders,
		"SecretStores":                     suite.testSecretStores,
		"KeyInASecretStore":                suite.testKeyInASecretStore,
		"ModelOfAnAgent":                   suite.testModelOfAnAgent,
		"CreateTurn":                       suite.testCreateTurn,
		"CreateTurnTakesOneOfTheMessagesSentAtOnce":         suite.testCreateTurnTakesOneOfTheMessagesSentAtOnce,
		"SupervisorsEndASessionWhileMessagesCome":           suite.testSupervisorsEndASessionWhileMessagesCome,
		"CreateTurnKeepsToTheMostThatMayWait":               suite.testCreateTurnKeepsToTheMostThatMayWait,
		"SupervisorsKeepToTheMostThatMayWaitTogether":       suite.testSupervisorsKeepToTheMostThatMayWaitTogether,
		"CreateTurnLetsThoseInThatHaveRoomToRun":            suite.testCreateTurnLetsThoseInThatHaveRoomToRun,
		"DispatchTurns":                                     suite.testDispatchTurns,
		"SupervisorsDispatchWithinTheLimitTogether":         suite.testSupervisorsDispatchWithinTheLimitTogether,
		"SupervisorsDispatchWithinTheLimitWhileTurnsCome":   suite.testSupervisorsDispatchWithinTheLimitWhileTurnsCome,
		"DispatchTurnsLeavesATurnThatWasCancelledMeanwhile": suite.testDispatchTurnsLeavesATurnThatWasCancelledMeanwhile,
		"DispatchTurnsCountsNoTurnThatWaitsForAPerson":      suite.testDispatchTurnsCountsNoTurnThatWaitsForAPerson,
		"AnnounceQueue":          suite.testAnnounceQueue,
		"FollowTurn":             suite.testFollowTurn,
		"TakeTurn":               suite.testTakeTurn,
		"TakeTurnThatIsNotToRun": suite.testTakeTurnThatIsNotToRun,
		"TakeTurnGivesTheTurnToOneOfTheRunnersThatAskAtOnce": suite.testTakeTurnGivesTheTurnToOneOfTheRunnersThatAskAtOnce,
		"SupervisorsEndATurnOnceTogether":                    suite.testSupervisorsEndATurnOnceTogether,
		"AnswerApprovalKeepsOneOfTheAnswersGivenAtOnce":      suite.testAnswerApprovalKeepsOneOfTheAnswersGivenAtOnce,
		"FollowTurnTellsWhetherTheTurnWaitsForAPerson":       suite.testFollowTurnTellsWhetherTheTurnWaitsForAPerson,
		"CallerThatWaitsNoLonger":                            suite.testCallerThatWaitsNoLonger,
		"StartTurn":                                          suite.testStartTurn,
		"CancelTurn":                                         suite.testCancelTurn,
		"ExpireTurns":                                        suite.testExpireTurns,
		"RequeueTurn":                                        suite.testRequeueTurn,
		"RequeuedTurnWaitsAsOneThatWasNeverDispatched":       suite.testRequeuedTurnWaitsAsOneThatWasNeverDispatched,
		"SupervisorsRequeueAndStartATurnAtOnce":              suite.testSupervisorsRequeueAndStartATurnAtOnce,
		"EndTurn":                                            suite.testEndTurn,
		"EndTurnKeepsWhatTheTurnSaid":                        suite.testEndTurnKeepsWhatTheTurnSaid,
		"SetFinished":                                        suite.testSetFinished,
		"Turns":                                              suite.testTurns,
		"DispatchedTurns":                                    suite.testDispatchedTurns,
		"NotFound":                                           suite.testNotFound,
		"Approvals":                                          suite.testApprovals,
	} {
		t.Run(name, test)
	}
}
