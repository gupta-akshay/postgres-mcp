package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJoinEnglish(t *testing.T) {
	cases := []struct {
		items []string
		want  string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"one"}, "one"},
		{[]string{"one", "two"}, "one and two"},
		{[]string{"one", "two", "three"}, "one, two, and three"},
		{[]string{"a", "b", "c", "d"}, "a, b, c, and d"},
	}

	for _, tc := range cases {
		got := joinEnglish(tc.items)
		assert.Equal(t, tc.want, got, "items=%v", tc.items)
	}
}
