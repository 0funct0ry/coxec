package cli

import (
	"reflect"
	"testing"
)

func TestParseUserVars(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "standard key=value",
			input: []string{"env=prod", "limit=500"},
			want:  map[string]string{"env": "prod", "limit": "500"},
		},
		{
			name:  "repeated keys",
			input: []string{"x=1", "x=2"},
			want:  map[string]string{"x": "2"},
		},
		{
			name:  "equals in value",
			input: []string{"filter=status=active"},
			want:  map[string]string{"filter": "status=active"},
		},
		{
			name:  "comma in value",
			input: []string{"filter=status=active,priority>=3"},
			want:  map[string]string{"filter": "status=active,priority>=3"},
		},
		{
			name:  "empty value",
			input: []string{"debug="},
			want:  map[string]string{"debug": ""},
		},
		{
			name:    "missing equals",
			input:   []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUserVars(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseUserVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseUserVars() = %v, want %v", got, tt.want)
			}
		})
	}
}
