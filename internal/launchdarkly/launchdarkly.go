package launchdarkly

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/superfly/flyctl/internal/logger"
	"github.com/superfly/flyctl/internal/tracing"
)

const clientSideID string = "6557a71bbffb5f134b84b15c"

type Client struct {
	ldContext  ldcontext.Context
	flags      map[string]FeatureFlag
	flagsMutex sync.Mutex
}

type contextKey struct{}

func NewContextWithClient(ctx context.Context, ldClient *Client) context.Context {
	return context.WithValue(ctx, contextKey{}, ldClient)
}

func ClientFromContext(ctx context.Context) *Client {
	client := ctx.Value(contextKey{})
	if client == nil {
		return nil
	}
	return client.(*Client)
}

type UserInfo struct {
	OrganizationID string
	UserID         int
}

func NewClient(ctx context.Context, userInfo UserInfo) (*Client, error) {
	_, span := tracing.GetTracer().Start(ctx, "new_feature_flag_client")
	defer span.End()

	orgID := 0

	if userInfo.OrganizationID != "" {
		var err error

		orgID, err = strconv.Atoi(userInfo.OrganizationID)
		if err != nil {
			return nil, err
		}
	}

	orgContext := ldcontext.NewBuilder("flyctl").Anonymous(true).SetInt("id", orgID).Kind(ldcontext.Kind("organization")).Build()
	userContext := ldcontext.NewBuilder("flyctl").Anonymous(true).SetInt("id", userInfo.UserID).Kind(ldcontext.Kind("user")).Build()

	launchDarklyContext := ldcontext.NewMultiBuilder().Add(orgContext).Add(userContext).Build()

	ldClient := &Client{ldContext: launchDarklyContext, flagsMutex: sync.Mutex{}}

	go ldClient.monitor(ctx)

	return ldClient, nil
}

func (ldClient *Client) monitor(ctx context.Context) {
	logger := logger.MaybeFromContext(ctx)

	for {
		err := ldClient.updateFeatureFlags()
		if err != nil && logger != nil {
			logger.Debug("Failed to update feature flags from LaunchDarkly: ", err)
		}

		// the launchdarkly docs recommend polling every 30 seconds
		time.Sleep(30 * time.Second)
	}
}

func (ldClient *Client) GetFeatureFlagValue(key string, defaultValue any) any {
	ldClient.flagsMutex.Lock()
	defer ldClient.flagsMutex.Unlock()

	if flag, ok := ldClient.flags[key]; ok {
		return flag.Value
	}
	return defaultValue

}

type FeatureFlag struct {
	FlagVersion int  `json:"flagVersion"`
	TrackEvents bool `json:"trackEvents"`
	Value       any  `json:"value"`
	Version     int  `json:"version"`
	Variation   int  `json:"variation"`
}

func (ldClient *Client) updateFeatureFlags() error {
	ldContextJSON := ldClient.ldContext.JSONString()
	ldContextB64 := base64.URLEncoding.EncodeToString([]byte(ldContextJSON))

	response, err := http.Get(fmt.Sprintf("https://clientsdk.launchdarkly.com/sdk/evalx/%s/contexts/%s", clientSideID, ldContextB64))
	if err != nil {
		return err
	}
	defer response.Body.Close()

	var flags map[string]FeatureFlag
	if err := json.NewDecoder(response.Body).Decode(&flags); err != nil {
		return err
	}

	if flags == nil {
		return nil
	}

	ldClient.flagsMutex.Lock()
	ldClient.flags = flags
	ldClient.flagsMutex.Unlock()

	return nil
}
