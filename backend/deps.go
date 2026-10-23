// Package backend pins runtime dependencies until service packages are added.
package backend

import (
	_ "github.com/go-chi/chi/v5"
	_ "github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/redis/go-redis/v9"
	_ "github.com/stretchr/testify/assert"
	_ "github.com/stretchr/testify/require"
)
