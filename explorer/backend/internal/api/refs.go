package api

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// refSet collects the addresses a response mentions, so their AddressRefs
// load in one query.
type refSet map[string]struct{}

func (rs refSet) add(addrs ...string) {
	for _, a := range addrs {
		if a != "" {
			rs[a] = struct{}{}
		}
	}
}

func (rs refSet) addPtr(addrs ...*string) {
	for _, a := range addrs {
		if a != nil && *a != "" {
			rs[*a] = struct{}{}
		}
	}
}

type refMap map[string]*AddressRef

// get never returns nil: an address the database does not know is a plain,
// unverified non-contract reference.
func (m refMap) get(a string) *AddressRef {
	if r, ok := m[a]; ok {
		c := *r
		return &c
	}
	return &AddressRef{Hash: EIP55(a)}
}

func (m refMap) getPtr(a *string) *AddressRef {
	if a == nil || *a == "" {
		return nil
	}
	return m.get(*a)
}

// loadRefs resolves names (label, then verified contract name, then token
// name), contract and verification flags and the token badge.
func (s *Server) loadRefs(ctx context.Context, rs refSet) (refMap, error) {
	out := refMap{}
	if len(rs) == 0 {
		return out, nil
	}
	list := make([]string, 0, len(rs))
	for a := range rs {
		list = append(list, a)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT x.address, COALESCE(a.is_contract, false), n.name, v.name, t.name, t.symbol, t.type
		FROM unnest($1::text[]) AS x(address)
		LEFT JOIN addresses a ON a.address = x.address
		LEFT JOIN address_names n ON n.address = x.address
		LEFT JOIN verified_contracts v ON v.address = x.address
		LEFT JOIN tokens t ON t.address = x.address`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		var isContract bool
		var label, vname, tname, tsym, ttype *string
		if err := rows.Scan(&addr, &isContract, &label, &vname, &tname, &tsym, &ttype); err != nil {
			return nil, err
		}
		ref := &AddressRef{Hash: EIP55(addr), IsContract: isContract, IsVerified: vname != nil}
		switch {
		case label != nil:
			ref.Name = label
		case vname != nil:
			ref.Name = vname
		case tname != nil:
			ref.Name = tname
		}
		if ttype != nil {
			ref.Token = &RefToken{Name: tname, Symbol: tsym, Type: *ttype}
			ref.IsContract = true
		}
		out[addr] = ref
	}
	return out, rows.Err()
}

const tokenCols = `t.address, t.type, t.is_erc3643, t.name, t.symbol, t.decimals, t.total_supply::text, t.holders_count, t.transfers_count, t.first_block`

func scanToken(row pgx.Row) (*Token, error) {
	var t Token
	var addr string
	var dec *int32
	if err := row.Scan(&addr, &t.Type, &t.IsERC3643, &t.Name, &t.Symbol, &dec, &t.TotalSupply, &t.HoldersCount, &t.TransfersCount, &t.FirstBlock); err != nil {
		return nil, err
	}
	t.Address = EIP55(addr)
	if dec != nil {
		d := int64(*dec)
		t.Decimals = &d
	}
	return &t, nil
}

func (s *Server) loadToken(ctx context.Context, addr string) (*Token, error) {
	t, err := scanToken(s.pool.QueryRow(ctx, `SELECT `+tokenCols+` FROM tokens t WHERE t.address = $1`, addr))
	if isNoRows(err) {
		return nil, nil
	}
	return t, err
}
