// Copyright 2018-2022 CERN
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package wopi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	appprovider "github.com/cs3org/go-cs3apis/cs3/app/provider/v1beta1"
	appregistry "github.com/cs3org/go-cs3apis/cs3/app/registry/v1beta1"
	authpb "github.com/cs3org/go-cs3apis/cs3/auth/provider/v1beta1"
	userpb "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	typespb "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	"github.com/cs3org/reva/pkg/app"
	"github.com/cs3org/reva/pkg/app/provider/registry"
	"github.com/cs3org/reva/pkg/appctx"
	"github.com/cs3org/reva/pkg/auth/scope"
	ctxpkg "github.com/cs3org/reva/pkg/ctx"
	"github.com/cs3org/reva/pkg/errtypes"
	"github.com/cs3org/reva/pkg/mime"
	"github.com/cs3org/reva/pkg/rgrpc/todo/pool"
	"github.com/cs3org/reva/pkg/rhttp"
	"github.com/cs3org/reva/pkg/sharedconf"
	"github.com/cs3org/reva/pkg/utils"
	"github.com/golang-jwt/jwt"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
)

func init() {
	registry.Register("wopi", New)
}

type config struct {
	MimeTypes           []string `mapstructure:"mime_types" docs:"nil;Inherited from the appprovider."`
	IOPSecret           string   `mapstructure:"iop_secret" docs:";The IOP secret used to connect to the wopiserver."`
	WopiURL             string   `mapstructure:"wopi_url" docs:";The wopiserver's URL."`
	AppName             string   `mapstructure:"app_name" docs:";The App user-friendly name."`
	AppIconURI          string   `mapstructure:"app_icon_uri" docs:";A URI to a static asset which represents the app icon."`
	FolderBaseURL       string   `mapstructure:"folder_base_url" docs:";The base URL to generate links to navigate back to the containing folder."`
	AppURL              string   `mapstructure:"app_url" docs:";The App URL."`
	AppIntURL           string   `mapstructure:"app_int_url" docs:";The internal app URL in case of dockerized deployments. Defaults to AppURL"`
	AppAPIKey           string   `mapstructure:"app_api_key" docs:";The API key used by the app, if applicable."`
	JWTSecret           string   `mapstructure:"jwt_secret" docs:";The JWT secret to be used to retrieve the token TTL."`
	AppDesktopOnly      bool     `mapstructure:"app_desktop_only" docs:"false;Specifies if the app can be opened only on desktop."`
	InsecureConnections bool     `mapstructure:"insecure_connections"`
}

func parseConfig(m map[string]interface{}) (*config, error) {
	c := &config{}
	if err := mapstructure.Decode(m, c); err != nil {
		return nil, err
	}
	return c, nil
}

type wopiProvider struct {
	conf       *config
	wopiClient *http.Client
	appURLs    map[string]map[string]string // map[viewMode]map[extension]appURL
}

// New returns an implementation of the app.Provider interface that
// connects to an application in the backend.
func New(m map[string]interface{}) (app.Provider, error) {
	c, err := parseConfig(m)
	if err != nil {
		return nil, err
	}

	if c.AppIntURL == "" {
		c.AppIntURL = c.AppURL
	}
	if c.IOPSecret == "" {
		c.IOPSecret = os.Getenv("REVA_APPPROVIDER_IOPSECRET")
	}
	c.JWTSecret = sharedconf.GetJWTSecret(c.JWTSecret)

	appURLs, err := getAppURLs(c)
	if err != nil {
		return nil, err
	}

	wopiClient := rhttp.GetHTTPClient(
		rhttp.Timeout(time.Duration(5*int64(time.Second))),
		rhttp.Insecure(c.InsecureConnections),
	)
	wopiClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &wopiProvider{
		conf:       c,
		wopiClient: wopiClient,
		appURLs:    appURLs,
	}, nil
}

func (p *wopiProvider) GetAppURL(ctx context.Context, resource *provider.ResourceInfo, viewMode appprovider.OpenInAppRequest_ViewMode, token string, opaqueMap map[string]*typespb.OpaqueEntry, language string) (*appprovider.OpenInAppURL, error) {
	log := appctx.GetLogger(ctx)

	ext := path.Ext(resource.Path)
	wopiurl, err := url.Parse(p.conf.WopiURL)
	if err != nil {
		return nil, err
	}
	wopiurl.Path = path.Join(wopiurl.Path, "/wopi/iop/openinapp")

	httpReq, err := rhttp.NewRequest(ctx, http.MethodGet, wopiurl.String(), nil)
	if err != nil {
		return nil, err
	}

	q := httpReq.URL.Query()
	q.Add("fileid", resource.GetId().OpaqueId)
	q.Add("endpoint", resource.GetId().StorageId)
	q.Add("viewmode", viewMode.String())
	q.Add("appname", p.conf.AppName)

	u, ok := ctxpkg.ContextGetUser(ctx)
	if !ok {
		// we must have been authenticated
		return nil, errors.New("wopi: ContextGetUser failed")
	}
	if u.Id.Type == userpb.UserType_USER_TYPE_LIGHTWEIGHT || u.Id.Type == userpb.UserType_USER_TYPE_FEDERATED {
		q.Add("userid", resource.Owner.OpaqueId+"@"+resource.Owner.Idp)
	} else {
		q.Add("userid", u.Id.OpaqueId+"@"+u.Id.Idp)
	}
	q.Add("username", u.DisplayName)

	scopes, ok := ctxpkg.ContextGetScopes(ctx)
	if !ok {
		// we must find at least one scope (as owner or sharee)
		return nil, errors.New("wopi: ContextGetScopes failed")
	}

	// TODO (lopresti) consolidate with the templating implemented in the edge branch;
	// here we assume the FolderBaseURL looks like `https://<hostname>` and we
	// either append `/files/spaces/<full_path>` or `/s/<pltoken>/<relative_path>`
	var rPath string
	if _, ok := utils.HasPublicShareRole(u); ok {
		// we are in a public link
		q.Del("username") // on public shares default to "Guest xyz"
		var err error
		rPath, err = getPathForPublicLink(ctx, scopes, resource)
		if err != nil {
			log.Warn().Err(err).Msg("wopi: failed to extract relative path from public link scope")
		}
	} else {
		// in all other cases use the resource's path
		rPath = "/files/spaces/" + path.Dir(resource.Path)
	}
	if rPath != "" {
		fu, err := url.JoinPath(p.conf.FolderBaseURL, rPath)
		if err != nil {
			log.Error().Err(err).Msg("wopi: failed to prepare folderurl parameter, folder_base_url may be malformed")
		} else {
			q.Add("folderurl", fu)
		}
	}

	var viewAppURL string
	if viewAppURLs, ok := p.appURLs["view"]; ok {
		if viewAppURL, ok = viewAppURLs[ext]; ok {
			q.Add("appviewurl", viewAppURL)
		}
	}
	var access = "edit"
	if resource.GetSize() == 0 {
		if _, ok := p.appURLs["editnew"]; ok {
			access = "editnew"
		}
	}
	if editAppURLs, ok := p.appURLs[access]; ok {
		if editAppURL, ok := editAppURLs[ext]; ok {
			q.Add("appurl", editAppURL)
		}
	}
	if q.Get("appurl") == "" {
		// assuming that a view action is always available in the /hosting/discovery manifest
		// eg. Collabora does support viewing jpgs but no editing
		// eg. OnlyOffice does support viewing pdfs but no editing
		// there is no known case of supporting edit only without view
		q.Add("appurl", viewAppURL)
	}
	if q.Get("appurl") == "" && q.Get("appviewurl") == "" {
		return nil, errors.New("wopi: neither edit nor view app url found")
	}
	if p.conf.AppIntURL != "" {
		q.Add("appinturl", p.conf.AppIntURL)
	}

	if _, ok := opaqueMap["forcelock"]; ok {
		// this is to work around an issue with Microsoft Office, cf. cs3org/wopiserver#106
		q.Add("forcelock", "1")
	}

	httpReq.URL.RawQuery = q.Encode()

	if p.conf.AppAPIKey != "" {
		httpReq.Header.Set("ApiKey", p.conf.AppAPIKey)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.conf.IOPSecret)
	httpReq.Header.Set("TokenHeader", token)

	log.Debug().Str("url", httpReq.URL.String()).Msg("Sending request to wopiserver")
	// Call the WOPI server and parse the response (body will always contain a payload)
	openRes, err := p.wopiClient.Do(httpReq)
	if err != nil {
		return nil, errors.Wrap(err, "wopi: error performing open request to wopiserver")
	}
	defer openRes.Body.Close()

	body, err := io.ReadAll(openRes.Body)
	if err != nil {
		return nil, err
	}
	if openRes.StatusCode != http.StatusOK {
		// WOPI returned failure: body contains a user-friendly error message (yet perform a sanity check)
		sbody := ""
		if body != nil {
			sbody = string(body)
		}
		log.Warn().Str("status", openRes.Status).Str("error", sbody).Msg("wopi: wopiserver returned error")
		return nil, errors.New(sbody)
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	tokenTTL, err := p.getAccessTokenTTL(ctx)
	if err != nil {
		return nil, err
	}

	appFullURL := result["app-url"].(string)

	if language != "" {
		url, err := url.Parse(appFullURL)
		if err != nil {
			return nil, err
		}
		urlQuery := url.Query()
		urlQuery.Set("ui", language)   // OnlyOffice + Office365
		urlQuery.Set("lang", language) // Collabora
		urlQuery.Set("rs", language)   // Office365, https://learn.microsoft.com/en-us/microsoft-365/cloud-storage-partner-program/online/discovery#dc_llcc
		url.RawQuery = urlQuery.Encode()
		appFullURL = url.String()
	}

	// Depending on whether the WOPI server returned any form parameters or not,
	// we decide whether the request method is POST or GET
	var formParams map[string]string
	method := http.MethodGet
	if form, ok := result["form-parameters"].(map[string]interface{}); ok {
		if tkn, ok := form["access_token"].(string); ok {
			formParams = map[string]string{
				"access_token":     tkn,
				"access_token_ttl": tokenTTL,
			}
			method = http.MethodPost
		}
	}

	log.Info().Str("url", appFullURL).Str("resource", resource.Path).Msg("wopi: returning URL for file")
	return &appprovider.OpenInAppURL{
		AppUrl:         appFullURL,
		Method:         method,
		FormParameters: formParams,
	}, nil
}

func (p *wopiProvider) GetAppProviderInfo(ctx context.Context) (*appregistry.ProviderInfo, error) {
	// Initially we store the mime types in a map to avoid duplicates
	mimeTypesMap := make(map[string]bool)
	for _, extensions := range p.appURLs {
		for ext := range extensions {
			m := mime.Detect(false, ext)
			mimeTypesMap[m] = true
		}
	}

	mimeTypes := make([]string, 0, len(mimeTypesMap))
	for m := range mimeTypesMap {
		mimeTypes = append(mimeTypes, m)
	}

	return &appregistry.ProviderInfo{
		Name:        p.conf.AppName,
		Icon:        p.conf.AppIconURI,
		DesktopOnly: p.conf.AppDesktopOnly,
		MimeTypes:   mimeTypes,
	}, nil
}

func getAppURLs(c *config) (map[string]map[string]string, error) {
	// Initialize WOPI URLs by discovery
	httpcl := rhttp.GetHTTPClient(
		rhttp.Timeout(time.Duration(5*int64(time.Second))),
		rhttp.Insecure(c.InsecureConnections),
	)

	appurl, err := url.Parse(c.AppIntURL)
	if err != nil {
		return nil, err
	}
	appurl.Path = path.Join(appurl.Path, "/hosting/discovery")

	discReq, err := http.NewRequest(http.MethodGet, appurl.String(), nil)
	if err != nil {
		return nil, err
	}
	discRes, err := httpcl.Do(discReq)
	if err != nil {
		return nil, err
	}
	defer discRes.Body.Close()

	var appURLs map[string]map[string]string

	if discRes.StatusCode == http.StatusOK {
		appURLs, err = parseWopiDiscovery(discRes.Body)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing wopi discovery response")
		}
	} else if discRes.StatusCode == http.StatusNotFound {
		// this may be a bridge-supported app
		discReq, err = http.NewRequest(http.MethodGet, c.AppIntURL, nil)
		if err != nil {
			return nil, err
		}
		discRes, err = httpcl.Do(discReq)
		if err != nil {
			return nil, err
		}
		defer discRes.Body.Close()

		buf := new(bytes.Buffer)
		_, err = buf.ReadFrom(discRes.Body)
		if err != nil {
			return nil, err
		}

		// scrape app's home page to find the appname
		if !strings.Contains(buf.String(), c.AppName) {
			return nil, fmt.Errorf("wopi: application server at %s does not match this AppProvider for %s", c.AppURL, c.AppName)
		}

		// TODO(lopresti) we don't know if the app is not supported/configured in WOPI
		// return nil, errors.New("Application server " + c.AppName + " running at " + c.AppURL + " is unsupported")

		// generate the map of supported extensions
		appURLs = make(map[string]map[string]string)
		appURLs["view"] = make(map[string]string)
		appURLs["edit"] = make(map[string]string)
		for _, m := range c.MimeTypes {
			exts := mime.GetFileExts(m)
			for _, e := range exts {
				appURLs["view"]["."+e] = c.AppURL
				appURLs["edit"]["."+e] = c.AppURL
			}
		}
	}
	return appURLs, nil
}

func (p *wopiProvider) getAccessTokenTTL(ctx context.Context) (string, error) {
	tkn := ctxpkg.ContextMustGetToken(ctx)
	token, err := jwt.ParseWithClaims(tkn, &jwt.StandardClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(p.conf.JWTSecret), nil
	})
	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(*jwt.StandardClaims); ok && token.Valid {
		// milliseconds since Jan 1, 1970 UTC as required in https://learn.microsoft.com/en-us/microsoft-365/cloud-storage-partner-program/rest/concepts#the-access_token_ttl-property
		return strconv.FormatInt(claims.ExpiresAt*1000, 10), nil
	}

	return "", errtypes.InvalidCredentials("wopi: invalid token present in ctx")
}

func parseWopiDiscovery(body io.Reader) (map[string]map[string]string, error) {
	appURLs := make(map[string]map[string]string)

	doc := etree.NewDocument()
	if _, err := doc.ReadFrom(body); err != nil {
		return nil, err
	}
	root := doc.SelectElement("wopi-discovery")
	if root == nil {
		return nil, errors.New("wopi-discovery response malformed")
	}

	for _, netzone := range root.SelectElements("net-zone") {
		if strings.Contains(netzone.SelectAttrValue("name", ""), "external") {
			for _, app := range netzone.SelectElements("app") {
				for _, action := range app.SelectElements("action") {
					access := action.SelectAttrValue("name", "")
					if access == "view" || access == "edit" || access == "editnew" {
						ext := action.SelectAttrValue("ext", "")
						urlString := action.SelectAttrValue("urlsrc", "")

						if ext == "" || urlString == "" {
							continue
						}

						u, err := url.Parse(urlString)
						if err != nil {
							// it sucks we cannot log here because this function is run
							// on init without any context.
							// TODO(labkode): add logging when we'll have static logging in boot phase.
							continue
						}

						// remove any malformed query parameter from discovery urls
						q := u.Query()
						for k := range q {
							if strings.Contains(k, "<") || strings.Contains(k, ">") {
								q.Del(k)
							}
						}

						u.RawQuery = q.Encode()

						if _, ok := appURLs[access]; !ok {
							appURLs[access] = make(map[string]string)
						}
						appURLs[access]["."+ext] = u.String()
					}
				}
			}
		}
	}
	return appURLs, nil
}

func getPathForPublicLink(ctx context.Context, scopes map[string]*authpb.Scope, resource *provider.ResourceInfo) (string, error) {
	pubShares, err := scope.GetPublicSharesFromScopes(scopes)
	if err != nil {
		return "", err
	}
	if len(pubShares) > 1 {
		return "", errors.New("More than one public share found in the scope, lookup not implemented")
	}

	client, err := pool.GetGatewayServiceClient(pool.Endpoint(sharedconf.GetGatewaySVC("")))
	if err != nil {
		return "", err
	}
	statRes, err := client.Stat(ctx, &provider.StatRequest{
		Ref: &provider.Reference{
			ResourceId: pubShares[0].ResourceId,
		},
	})
	if err != nil {
		return "", err
	}

	if statRes.Info.Path == resource.Path {
		// this is a direct link to the resource
		return "/s/" + pubShares[0].Token, nil
	}
	// otherwise we are in a subfolder of the public link
	relPath, err := filepath.Rel(statRes.Info.Path, resource.Path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(relPath, "../") {
		return "", errors.New("Scope path does not contain target resource")
	}
	return path.Join("/files/public/show/"+pubShares[0].Token, path.Dir(relPath)), nil
}
