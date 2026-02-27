package mqtt

import "fmt"

type Client struct {
	BrokerURL string
}

func NewClient(brokerURL string) *Client {
	return &Client{BrokerURL: brokerURL}
}

func (c *Client) Close() error { return nil }

func TopicLocationPub(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/pub/%s", providerID)
}

func TopicLocationLocal(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/local/%s", providerID)
}

func TopicLocationEPSG4326(providerID string) string {
	return fmt.Sprintf("/omlox/json/location_updates/epsg4326/%s", providerID)
}

func TopicProximity(source, providerID string) string {
	return fmt.Sprintf("/omlox/json/proximity_updates/%s/%s", source, providerID)
}
