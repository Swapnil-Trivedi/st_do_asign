package main

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type mongoPolicy struct {
	db *mongo.Database
}

func newMongoPolicy(ctx context.Context, uri, dbName string) (*mongoPolicy, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &mongoPolicy{db: client.Database(dbName)}, nil
}

func (m *mongoPolicy) Close(ctx context.Context) error {
	return m.db.Client().Disconnect(ctx)
}

type configDoc struct {
	ID        primitive.ObjectID `bson:"_id"`
	Name      string             `bson:"name"`
	Algorithm string             `bson:"algorithm"`
	Params    map[string]any     `bson:"params"`
	Enabled   bool               `bson:"enabled"`
}

type namespaceDoc struct {
	ID           primitive.ObjectID  `bson:"_id"`
	Label        string              `bson:"label"`
	UpstreamHost string              `bson:"upstreamHost"`
	Path         *string             `bson:"path"`
	Algorithm    string              `bson:"algorithm"`
	ConfigID     primitive.ObjectID  `bson:"configId"`
	MatchMode    string              `bson:"matchMode"`
	Priority     int                 `bson:"priority"`
	Enabled      bool                `bson:"enabled"`
}

type ruleDoc struct {
	ID          primitive.ObjectID `bson:"_id"`
	NamespaceID primitive.ObjectID `bson:"namespaceId"`
	Label       string             `bson:"label"`
	Enabled     bool               `bson:"enabled"`
	Match       ruleMatchDoc       `bson:"match"`
}

type ruleMatchDoc struct {
	Kind    string   `bson:"kind"`
	Name    string   `bson:"name,omitempty"`
	Op      string   `bson:"op,omitempty"`
	Value   string   `bson:"value,omitempty"`
	Values  []string `bson:"values,omitempty"`
	Pattern string   `bson:"pattern,omitempty"`
	Attr    string   `bson:"attr,omitempty"`
	Field   string   `bson:"field,omitempty"`
}

func (m *mongoPolicy) LoadSnapshot(ctx context.Context) (*PolicySnapshot, error) {
	cfgCur, err := m.db.Collection("algorithm_configs").Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return nil, fmt.Errorf("configs: %w", err)
	}
	defer cfgCur.Close(ctx)

	configs := map[string]AlgorithmConfig{}
	for cfgCur.Next(ctx) {
		var d configDoc
		if err := cfgCur.Decode(&d); err != nil {
			return nil, err
		}
		configs[d.ID.Hex()] = AlgorithmConfig{
			ID: d.ID.Hex(), Name: d.Name, Algorithm: d.Algorithm,
			Params: d.Params, Enabled: d.Enabled,
		}
	}
	if err := cfgCur.Err(); err != nil {
		return nil, err
	}

	algoCur, err := m.db.Collection("algorithms").Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return nil, fmt.Errorf("algorithms: %w", err)
	}
	defer algoCur.Close(ctx)
	enabledAlgos := map[string]bool{}
	for algoCur.Next(ctx) {
		var a struct {
			ID string `bson:"_id"`
		}
		if err := algoCur.Decode(&a); err != nil {
			return nil, err
		}
		enabledAlgos[a.ID] = true
	}
	if err := algoCur.Err(); err != nil {
		return nil, err
	}

	nsCur, err := m.db.Collection("namespaces").Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return nil, fmt.Errorf("namespaces: %w", err)
	}
	defer nsCur.Close(ctx)

	var nsDocs []namespaceDoc
	if err := nsCur.All(ctx, &nsDocs); err != nil {
		return nil, err
	}

	ruleCur, err := m.db.Collection("rules").Find(ctx, bson.M{"enabled": true})
	if err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	defer ruleCur.Close(ctx)

	rulesByNS := map[string][]Rule{}
	for ruleCur.Next(ctx) {
		var d ruleDoc
		if err := ruleCur.Decode(&d); err != nil {
			return nil, err
		}
		nsID := d.NamespaceID.Hex()
		rulesByNS[nsID] = append(rulesByNS[nsID], Rule{
			ID: d.ID.Hex(), NamespaceID: nsID, Label: d.Label, Enabled: d.Enabled,
			Match: RuleMatch{
				Kind: d.Match.Kind, Name: d.Match.Name, Op: d.Match.Op,
				Value: d.Match.Value, Values: d.Match.Values,
				Pattern: d.Match.Pattern, Attr: d.Match.Attr, Field: d.Match.Field,
			},
		})
	}
	if err := ruleCur.Err(); err != nil {
		return nil, err
	}

	out := &PolicySnapshot{LoadedAt: time.Now().UTC()}
	for _, d := range nsDocs {
		cfg, ok := configs[d.ConfigID.Hex()]
		if !ok || !cfg.Enabled {
			continue
		}
		if !enabledAlgos[cfg.Algorithm] {
			continue
		}
		algo := d.Algorithm
		if algo == "" {
			algo = cfg.Algorithm
		}
		matchMode := d.MatchMode
		if matchMode == "" {
			matchMode = MatchModeAll
		}
		priority := d.Priority
		if priority == 0 {
			priority = 100
		}
		nsID := d.ID.Hex()
		out.Namespaces = append(out.Namespaces, CompiledNamespace{
			Namespace: Namespace{
				ID: nsID, Label: d.Label, UpstreamHost: d.UpstreamHost, Path: d.Path,
				Algorithm: algo, ConfigID: d.ConfigID.Hex(), MatchMode: matchMode,
				Priority: priority, Enabled: d.Enabled,
			},
			Rules:  rulesByNS[nsID],
			Config: cfg,
		})
	}
	return out, nil
}
