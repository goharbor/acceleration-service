package test

import (
	"net/http"
)

func (suite *IntegrationTestSuite) TestNydusConversion() {
	resp, err := suite.client.Request(http.MethodPost, "/api/v1/conversions", nil, nil)
	suite.Require().NoError(err)
	suite.Require().Equal(http.StatusOK, resp.StatusCode)
}
