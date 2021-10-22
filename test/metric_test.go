package test

import (
	"io/ioutil"
	"net/http"
)

func (suite *IntegrationTestSuite) TestMetric() {
	resp, err := suite.client.Request(http.MethodGet, "/metrics", nil, nil)
	suite.Require().NoError(err)
	defer resp.Body.Close()
	suite.Require().Equal(http.StatusOK, resp.StatusCode)

	_, err = ioutil.ReadAll(resp.Body)
	suite.Require().NoError(err)
}
