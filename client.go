package growthbook

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"

	"github.com/growthbook/growthbook-golang/internal/value"
)

const defaultApiHost = "https://cdn.growthbook.io"

var (
	ErrNoDecryptionKey = errors.New("No decryption key provided")
)

type Client struct {
	data             *data
	enabled          bool
	attributes       value.ObjValue
	url              *url.URL
	forcedVariations ForcedVariationsMap
	qaMode           bool
	trackingCallback (TrackingCallback)
	logger           *slog.Logger
}

// ForcedVariationsMap is a map that forces an Experiment to always assign a specific variation. Useful for QA.
type ForcedVariationsMap map[string]int

// TrackingCallback function that is executed every time a user is included in an Experiment.
type TrackingCallback func(*Experiment, *ExperimentResult)

func NewApiClient(apiHost string, clientKey string) (*Client, error) {
	ctx := context.Background()
	return NewClient(ctx, WithApiHost(apiHost), WithClientKey(clientKey))
}

func NewClient(ctx context.Context, opts ...ClientOption) (*Client, error) {
	client := defaultClient()
	for _, opt := range opts {
		err := opt(client)
		if err != nil {
			return nil, err
		}
	}

	if client.data.dataSource != nil {
		go client.startDataSource(ctx)
	}

	return client, nil
}

func (client *Client) Close() error {
	ds := client.data.dataSource
	if ds == nil || !client.data.dsStarted {
		return nil
	}
	return ds.Close()
}

func defaultClient() *Client {
	return &Client{
		data:    newData(),
		enabled: true,
		qaMode:  false,
		logger:  slog.Default(),
	}
}

// SetFeatures updates shared client features.
func (client *Client) SetFeatures(features FeatureMap) error {
	client.data.withLock(func(d *data) error {
		d.features = features
		return nil
	})
	return nil
}

// SetJSONFeatures updates shared features from JSON
func (client *Client) SetJSONFeatures(featuresJSON string) error {
	var features FeatureMap
	err := json.Unmarshal([]byte(featuresJSON), &features)
	if err != nil {
		return err
	}
	return client.SetFeatures(features)
}

// SetEncryptedJSONFeatures updates shared features from encrypted JSON.
// Uses client's decryption key.
func (client *Client) SetEncryptedJSONFeatures(encryptedJSON string) error {
	if client.data.decryptionKey == "" {
		return ErrNoDecryptionKey
	}
	featuresJSON, err := decrypt(encryptedJSON, client.data.decryptionKey)
	if err != nil {
		return err
	}
	return client.SetJSONFeatures(featuresJSON)
}

// UpdateFromApiResponse updates shared data from Growthbook API response
func (client *Client) UpdateFromApiResponse(resp *FeatureApiResponse) error {
	var features FeatureMap
	var err error
	if resp.EncryptedFeatures != "" {
		features, err = client.DecryptFeatures(resp.EncryptedFeatures)
		if err != nil {
			return err
		}
	} else {
		features = resp.Features
	}
	client.data.withLock(func(d *data) error {
		d.features = features
		d.savedGroups = resp.SavedGroups
		return nil
	})
	return nil
}

func (client *Client) DecryptFeatures(encrypted string) (FeatureMap, error) {
	var features FeatureMap
	featuresJSON, err := client.data.decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(featuresJSON), &features)
	if err != nil {
		return nil, err
	}
	return features, err
}

func (client *Client) UpdateFromApiResponseJSON(respJSON string) error {
	var resp FeatureApiResponse
	err := json.Unmarshal([]byte(respJSON), &resp)
	if err != nil {
		return err
	}
	return client.UpdateFromApiResponse(&resp)
}

// EvalFeature evaluates feature based on attributes and features map
func (client *Client) EvalFeature(ctx context.Context, key string) *FeatureResult {
	e := client.evaluator()
	return e.evalFeature(key)
}

func (client *Client) RunExperiment(ctx context.Context, exp *Experiment) *ExperimentResult {
	e := client.evaluator()
	return e.runExperiment(exp, "")
}

func (client *Client) Features() FeatureMap {
	return client.data.getFeatures()
}

// Internals
func (client *Client) evaluator() *evaluator {
	client.data.mu.RLock()
	e := evaluator{
		features:    client.data.features,
		savedGroups: client.data.savedGroups,
		client:      client,
	}
	client.data.mu.RUnlock()
	return &e
}

func (client *Client) clone() *Client {
	c := *client
	return &c
}
