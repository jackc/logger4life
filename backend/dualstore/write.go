package dualstore

import "context"

func (s *Store) ClaimWrite(ctx context.Context, u, o, k, h string) (string, error) {
	return compareCall("ClaimWrite", func() (string, error) { return s.primary.ClaimWrite(ctx, u, o, k, h) }, func() (string, error) { return s.secondary.ClaimWrite(ctx, u, o, k, h) })
}
func (s *Store) SaveWrite(ctx context.Context, u, o, k, r string) error {
	return compareError("SaveWrite", func() error { return s.primary.SaveWrite(ctx, u, o, k, r) }, func() error { return s.secondary.SaveWrite(ctx, u, o, k, r) })
}
