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

type mongoStore struct {
	db *mongo.Database
}

func newMongoStore(ctx context.Context, uri, dbName string) (*mongoStore, error) {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	s := &mongoStore{db: client.Database(dbName)}
	if err := s.ensureIndexes(ctx); err != nil {
		return nil, err
	}
	if err := s.ensureAlgorithms(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *mongoStore) col(name string) *mongo.Collection { return s.db.Collection(name) }

func (s *mongoStore) ensureIndexes(ctx context.Context) error {
	_, _ = s.col("algorithm_configs").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true),
	})
	_, _ = s.col("namespaces").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "label", Value: 1}}, Options: options.Index().SetUnique(true),
	})
	_, _ = s.col("rules").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "namespaceId", Value: 1}, {Key: "enabled", Value: 1}},
	})
	return nil
}

func (s *mongoStore) ensureAlgorithms(ctx context.Context) error {
	n, err := s.col("algorithms").CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	docs := make([]any, 0, 3)
	for _, a := range defaultAlgorithms() {
		docs = append(docs, a)
	}
	_, err = s.col("algorithms").InsertMany(ctx, docs)
	return err
}

func oid(id string) (primitive.ObjectID, error) {
	return primitive.ObjectIDFromHex(id)
}

func (s *mongoStore) ListAlgorithms(ctx context.Context) ([]Algorithm, error) {
	cur, err := s.col("algorithms").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []Algorithm
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type configDoc struct {
	ID        primitive.ObjectID `bson:"_id"`
	Name      string             `bson:"name"`
	Algorithm string             `bson:"algorithm"`
	Params    map[string]any     `bson:"params"`
	Enabled   bool               `bson:"enabled"`
	CreatedAt time.Time          `bson:"createdAt"`
	UpdatedAt time.Time          `bson:"updatedAt"`
}

func (d configDoc) toModel() AlgorithmConfig {
	return AlgorithmConfig{
		ID: d.ID.Hex(), Name: d.Name, Algorithm: d.Algorithm, Params: d.Params,
		Enabled: d.Enabled, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStore) ListConfigs(ctx context.Context) ([]AlgorithmConfig, error) {
	cur, err := s.col("algorithm_configs").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []configDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]AlgorithmConfig, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.toModel())
	}
	return out, nil
}

func (s *mongoStore) GetConfig(ctx context.Context, id string) (AlgorithmConfig, error) {
	oid, err := oid(id)
	if err != nil {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	var d configDoc
	err = s.col("algorithm_configs").FindOne(ctx, bson.M{"_id": oid}).Decode(&d)
	if err == mongo.ErrNoDocuments {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	if err != nil {
		return AlgorithmConfig{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) CreateConfig(ctx context.Context, c AlgorithmConfig) (AlgorithmConfig, error) {
	id := primitive.NewObjectID()
	now := time.Now().UTC()
	d := configDoc{
		ID: id, Name: c.Name, Algorithm: c.Algorithm, Params: c.Params,
		Enabled: c.Enabled, CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.col("algorithm_configs").InsertOne(ctx, d)
	if mongo.IsDuplicateKeyError(err) {
		return AlgorithmConfig{}, fmt.Errorf("config name already exists")
	}
	if err != nil {
		return AlgorithmConfig{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) UpdateConfig(ctx context.Context, c AlgorithmConfig) (AlgorithmConfig, error) {
	id, err := oid(c.ID)
	if err != nil {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	now := time.Now().UTC()
	res, err := s.col("algorithm_configs").UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{
			"name": c.Name, "algorithm": c.Algorithm, "params": c.Params,
			"enabled": c.Enabled, "updatedAt": now,
		},
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return AlgorithmConfig{}, fmt.Errorf("config name already exists")
		}
		return AlgorithmConfig{}, err
	}
	if res.MatchedCount == 0 {
		return AlgorithmConfig{}, fmt.Errorf("config not found")
	}
	return s.GetConfig(ctx, c.ID)
}

func (s *mongoStore) DeleteConfig(ctx context.Context, id string) error {
	oid, err := oid(id)
	if err != nil {
		return fmt.Errorf("config not found")
	}
	res, err := s.col("algorithm_configs").DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("config not found")
	}
	return nil
}

type nsDoc struct {
	ID           primitive.ObjectID `bson:"_id"`
	Label        string             `bson:"label"`
	UpstreamHost string             `bson:"upstreamHost"`
	Path         *string            `bson:"path"`
	Algorithm    string             `bson:"algorithm"`
	ConfigID     primitive.ObjectID `bson:"configId"`
	MatchMode    string             `bson:"matchMode"`
	Priority     int                `bson:"priority"`
	Enabled      bool               `bson:"enabled"`
	CreatedAt    time.Time          `bson:"createdAt"`
	UpdatedAt    time.Time          `bson:"updatedAt"`
}

func (d nsDoc) toModel() Namespace {
	return Namespace{
		ID: d.ID.Hex(), Label: d.Label, UpstreamHost: d.UpstreamHost, Path: d.Path,
		Algorithm: d.Algorithm, ConfigID: d.ConfigID.Hex(), MatchMode: d.MatchMode,
		Priority: d.Priority, Enabled: d.Enabled, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStore) ListNamespaces(ctx context.Context) ([]Namespace, error) {
	cur, err := s.col("namespaces").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []nsDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]Namespace, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.toModel())
	}
	return out, nil
}

func (s *mongoStore) GetNamespace(ctx context.Context, id string) (Namespace, error) {
	oid, err := oid(id)
	if err != nil {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	var d nsDoc
	err = s.col("namespaces").FindOne(ctx, bson.M{"_id": oid}).Decode(&d)
	if err == mongo.ErrNoDocuments {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	if err != nil {
		return Namespace{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) CreateNamespace(ctx context.Context, n Namespace) (Namespace, error) {
	cfgOID, err := oid(n.ConfigID)
	if err != nil {
		return Namespace{}, fmt.Errorf("config not found")
	}
	id := primitive.NewObjectID()
	now := time.Now().UTC()
	d := nsDoc{
		ID: id, Label: n.Label, UpstreamHost: n.UpstreamHost, Path: n.Path,
		Algorithm: n.Algorithm, ConfigID: cfgOID, MatchMode: n.MatchMode,
		Priority: n.Priority, Enabled: n.Enabled, CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.col("namespaces").InsertOne(ctx, d)
	if mongo.IsDuplicateKeyError(err) {
		return Namespace{}, fmt.Errorf("namespace label already exists")
	}
	if err != nil {
		return Namespace{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) UpdateNamespace(ctx context.Context, n Namespace) (Namespace, error) {
	id, err := oid(n.ID)
	if err != nil {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	cfgOID, err := oid(n.ConfigID)
	if err != nil {
		return Namespace{}, fmt.Errorf("config not found")
	}
	now := time.Now().UTC()
	res, err := s.col("namespaces").UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{
			"label": n.Label, "upstreamHost": n.UpstreamHost, "path": n.Path,
			"algorithm": n.Algorithm, "configId": cfgOID, "matchMode": n.MatchMode,
			"priority": n.Priority, "enabled": n.Enabled, "updatedAt": now,
		},
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return Namespace{}, fmt.Errorf("namespace label already exists")
		}
		return Namespace{}, err
	}
	if res.MatchedCount == 0 {
		return Namespace{}, fmt.Errorf("namespace not found")
	}
	return s.GetNamespace(ctx, n.ID)
}

func (s *mongoStore) DeleteNamespace(ctx context.Context, id string) error {
	oid, err := oid(id)
	if err != nil {
		return fmt.Errorf("namespace not found")
	}
	_, _ = s.col("rules").DeleteMany(ctx, bson.M{"namespaceId": oid})
	res, err := s.col("namespaces").DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("namespace not found")
	}
	return nil
}

type ruleDoc struct {
	ID          primitive.ObjectID `bson:"_id"`
	NamespaceID primitive.ObjectID `bson:"namespaceId"`
	Label       string             `bson:"label"`
	Enabled     bool               `bson:"enabled"`
	Match       RuleMatch          `bson:"match"`
	CreatedAt   time.Time          `bson:"createdAt"`
	UpdatedAt   time.Time          `bson:"updatedAt"`
}

func (d ruleDoc) toModel() Rule {
	return Rule{
		ID: d.ID.Hex(), NamespaceID: d.NamespaceID.Hex(), Label: d.Label,
		Enabled: d.Enabled, Match: d.Match, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *mongoStore) ListRules(ctx context.Context, namespaceID string) ([]Rule, error) {
	nsOID, err := oid(namespaceID)
	if err != nil {
		return nil, fmt.Errorf("namespace not found")
	}
	cur, err := s.col("rules").Find(ctx, bson.M{"namespaceId": nsOID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []ruleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.toModel())
	}
	return out, nil
}

func (s *mongoStore) GetRule(ctx context.Context, id string) (Rule, error) {
	oid, err := oid(id)
	if err != nil {
		return Rule{}, fmt.Errorf("rule not found")
	}
	var d ruleDoc
	err = s.col("rules").FindOne(ctx, bson.M{"_id": oid}).Decode(&d)
	if err == mongo.ErrNoDocuments {
		return Rule{}, fmt.Errorf("rule not found")
	}
	if err != nil {
		return Rule{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	nsOID, err := oid(r.NamespaceID)
	if err != nil {
		return Rule{}, fmt.Errorf("namespace not found")
	}
	var ns nsDoc
	if err := s.col("namespaces").FindOne(ctx, bson.M{"_id": nsOID}).Decode(&ns); err != nil {
		return Rule{}, fmt.Errorf("namespace not found")
	}
	id := primitive.NewObjectID()
	now := time.Now().UTC()
	d := ruleDoc{
		ID: id, NamespaceID: nsOID, Label: r.Label, Enabled: r.Enabled,
		Match: r.Match, CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.col("rules").InsertOne(ctx, d)
	if err != nil {
		return Rule{}, err
	}
	return d.toModel(), nil
}

func (s *mongoStore) UpdateRule(ctx context.Context, r Rule) (Rule, error) {
	id, err := oid(r.ID)
	if err != nil {
		return Rule{}, fmt.Errorf("rule not found")
	}
	now := time.Now().UTC()
	res, err := s.col("rules").UpdateOne(ctx, bson.M{"_id": id}, bson.M{
		"$set": bson.M{"label": r.Label, "enabled": r.Enabled, "match": r.Match, "updatedAt": now},
	})
	if err != nil {
		return Rule{}, err
	}
	if res.MatchedCount == 0 {
		return Rule{}, fmt.Errorf("rule not found")
	}
	return s.GetRule(ctx, r.ID)
}

func (s *mongoStore) DeleteRule(ctx context.Context, id string) error {
	oid, err := oid(id)
	if err != nil {
		return fmt.Errorf("rule not found")
	}
	res, err := s.col("rules").DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return fmt.Errorf("rule not found")
	}
	return nil
}

func (s *mongoStore) CountNamespacesByConfig(ctx context.Context, configID string) (int, error) {
	oid, err := oid(configID)
	if err != nil {
		return 0, nil
	}
	n, err := s.col("namespaces").CountDocuments(ctx, bson.M{"configId": oid})
	return int(n), err
}
