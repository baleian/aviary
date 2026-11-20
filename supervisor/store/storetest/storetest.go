// Package storetest is the test suite that every store.Store must pass. The
// tests of each plugin run it against their kind of database.
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

// Database is an empty database for one test.
type Database interface {
	// Open returns a new Store on the database, as one more Supervisor opens
	// it. The Store is closed when the test ends.
	Open(t *testing.T) store.Store
	// Age moves all recorded times of the turns and of their approval
	// requests back by the given duration.
	Age(t *testing.T, by time.Duration, turns ...uuid.UUID)
}

// keeps reports whether kept is the same JSON value as given, allowing U+FFFD
// in place of U+0000, which a database may be unable to store.
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

// withoutNull returns the JSON value with every U+0000 replaced by U+FFFD.
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

// keepsAll reports whether kept matches given, value by value, as keeps does.
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

type suite struct {
	create func(t *testing.T) Database
}

// Run runs the suite, with a database from create for every test, since the
// tests count the turns in their database.
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
		"TurnOfASession":                   suite.testTurnOfASession,
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
		"DispatchTurnsCountsATurnThatWaitsForAPerson":       suite.testDispatchTurnsCountsATurnThatWaitsForAPerson,
		"AnnounceQueue":                   suite.testAnnounceQueue,
		"FollowTurn":                      suite.testFollowTurn,
		"SupervisorsEndATurnOnceTogether": suite.testSupervisorsEndATurnOnceTogether,
		"AnswerApprovalKeepsOneOfTheAnswersGivenAtOnce": suite.testAnswerApprovalKeepsOneOfTheAnswersGivenAtOnce,
		"CallerThatWaitsNoLonger":                       suite.testCallerThatWaitsNoLonger,
		"StartTurn":                                     suite.testStartTurn,
		"CancelTurn":                                    suite.testCancelTurn,
		"ExpireTurns":                                   suite.testExpireTurns,
		"EndTurn":                                       suite.testEndTurn,
		"EndTurnKeepsWhatTheTurnSaid":                   suite.testEndTurnKeepsWhatTheTurnSaid,
		"SetFinished":                                   suite.testSetFinished,
		"Turns":                                         suite.testTurns,
		"DispatchedTurns":                               suite.testDispatchedTurns,
		"NotFound":                                      suite.testNotFound,
		"Approvals":                                     suite.testApprovals,
	} {
		t.Run(name, test)
	}
}
