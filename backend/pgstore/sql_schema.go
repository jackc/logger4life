package pgstore

import (
	"context"
	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ListSQLSchemaViews(ctx context.Context) ([]*core.SQLSchemaView, error) {
	rows, e := s.conn(ctx).Query(ctx, `SELECT c.relname,obj_description(c.oid,'pg_class'),a.attname,format_type(a.atttypid,a.atttypmod),col_description(a.attrelid,a.attnum) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid WHERE n.nspname='sql_query' AND c.relkind='v' AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	idx := map[string]*core.SQLSchemaView{}
	out := []*core.SQLSchemaView{}
	for rows.Next() {
		var vn, cn, dt string
		var vc, cc *string
		if e := rows.Scan(&vn, &vc, &cn, &dt, &cc); e != nil {
			return nil, e
		}
		v := idx[vn]
		if v == nil {
			v = &core.SQLSchemaView{Name: vn, Comment: vc, Columns: []core.SQLSchemaColumn{}}
			idx[vn] = v
			out = append(out, v)
		}
		v.Columns = append(v.Columns, core.SQLSchemaColumn{Name: cn, DataType: dt, Comment: cc})
	}
	return out, rows.Err()
}
