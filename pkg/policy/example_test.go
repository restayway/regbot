package policy_test

import (
	"fmt"

	"github.com/restayway/regbot/pkg/policy"
)

func ExampleParseCalendar() {
	version, err := policy.ParseCalendar("v2026.07.20.3-api")
	if err != nil {
		panic(err)
	}
	fmt.Println(version.Date.Format("2006-01-02"), version.Sequence, version.App)
	// Output: 2026-07-20 3 api
}
