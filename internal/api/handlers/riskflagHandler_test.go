package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestRiskFlagFilterFromQuery(t *testing.T) {
	tests := []struct {
		name, query string
		wantErr     bool
	}{
		{name: "valid filters", query: "status=IN_REVIEW&level=high&customer_id=12&transaction_id=34&q=Marie&limit=50&offset=20"},
		{name: "invalid status", query: "status=UNKNOWN", wantErr: true},
		{name: "invalid level", query: "level=SEVERE", wantErr: true},
		{name: "invalid limit", query: "limit=0", wantErr: true},
		{name: "invalid offset", query: "offset=-1", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/risk-flags?"+test.query, nil)
			filter, err := riskFlagFilterFromQuery(req)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (filter.Status != "IN_REVIEW" || filter.Level != "HIGH" || filter.CustomerID != 12 || filter.TransactionID != 34 || filter.Limit != 50 || filter.Offset != 20) {
				t.Fatalf("unexpected filter: %+v", filter)
			}
		})
	}
}
