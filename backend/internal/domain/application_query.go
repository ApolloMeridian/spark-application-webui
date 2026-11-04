package domain

type ApplicationQuery struct {
	Namespaces                                                []string
	Keyword, State, Owner, Namespace, Sort, Direction, UserID string
	Favorite                                                  bool
	Page, PageSize                                            int
}
