package dbexec

import "testing"

func TestPGRenderViewDDL(t *testing.T) {
	tests := []struct {
		name         string
		materialized bool
		definition   string
		want         string
	}{
		{
			name:       "view",
			definition: "SELECT id, name FROM public.mango_shop;",
			want:       "-- postgres view: \"public\".\"v_qianchongjia_rectification_task\"\nCREATE VIEW \"public\".\"v_qianchongjia_rectification_task\" AS\nSELECT id, name FROM public.mango_shop;\n",
		},
		{
			name:         "materialized view",
			materialized: true,
			definition:   " SELECT id FROM public.mango_shop ; ",
			want:         "-- postgres materialized view: \"public\".\"v_shop\"\nCREATE MATERIALIZED VIEW \"public\".\"v_shop\" AS\nSELECT id FROM public.mango_shop;\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pgRenderViewDDL("public", map[bool]string{true: "v_shop", false: "v_qianchongjia_rectification_task"}[tt.materialized], tt.materialized, tt.definition); got != tt.want {
				t.Fatalf("pgRenderViewDDL() = %q, want %q", got, tt.want)
			}
		})
	}
}
