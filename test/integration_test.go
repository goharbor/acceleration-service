package test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/goharbor/acceleration-service/pkg/client"
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/daemon"
)

type IntegrationTestSuite struct {
	client *client.Client
	suite.Suite
}

func (suite *IntegrationTestSuite) startDaemon(cfg *config.Config) {
	d, err := daemon.NewDaemon(cfg)
	suite.Require().NoError(err)

	go func() {
		err = d.Run()
		suite.Require().NoError(err)
	}()
}

func (suite *IntegrationTestSuite) newClient(cfg *config.Config) *client.Client {
	return client.NewClient(fmt.Sprintf("127.0.0.1:%s", cfg.Server.Port))
}

func (suite *IntegrationTestSuite) cleanUp() {
}

func (suite *IntegrationTestSuite) SetupSuite() {
	suite.cleanUp()

	cfg, err := config.Parse("../misc/config/config.yaml.nydus.tmpl")
	suite.Require().NoError(err)
	suite.startDaemon(cfg)

	// Wait for daemon to finish booting.
	time.Sleep(3 * time.Second)

	suite.client = suite.newClient(cfg)
}

func TestIntegration(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}
