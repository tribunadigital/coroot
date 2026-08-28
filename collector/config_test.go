package collector

import (
	"encoding/json"
	"math/rand"
	"testing"

	"inet.af/netaddr"

	"github.com/stretchr/testify/assert"

	"github.com/coroot/coroot/model"
	"github.com/coroot/coroot/timeseries"
)

func TestSelectIP(t *testing.T) {
	check := func(expected string, from ...string) {
		var ips []netaddr.IP
		for _, s := range from {
			ips = append(ips, netaddr.MustParseIP(s))
		}
		for i := 0; i < 100; i++ {
			rand.Shuffle(len(ips), func(i, j int) {
				ips[i], ips[j] = ips[j], ips[i]
			})
			assert.Equal(t, expected, SelectIP(ips).String())
		}
	}

	assert.Nil(t, SelectIP(nil))

	check("127.0.0.1", "127.0.0.1")
	check("192.168.0.1", "127.0.0.1", "192.168.0.1")
	check("192.168.0.1", "127.0.0.1", "192.168.0.2", "192.168.0.1")
	check("8.8.8.8", "127.0.0.1", "8.8.8.8")
	check("1.1.1.1", "127.0.0.1", "8.8.8.8", "1.1.1.1")
	check("192.168.0.1", "127.0.0.1", "8.8.8.8", "192.168.0.1")
	check("100.64.0.1", "127.0.0.1", "8.8.8.8", "192.168.0.1", "100.64.0.1")
	check("172.17.0.1", "127.0.0.1", "172.17.0.1", "172.17.0.3", "172.18.0.1")
}

func TestSniHostname(t *testing.T) {
	newNode := func(name string) *model.Node {
		node := model.NewNode("cluster", model.NewNodeId("machine-id", "machine-id"))
		ts := timeseries.New(0, 1, 15)
		ts.Set(15, 1)
		node.Name.Update(ts, name)
		return node
	}
	newInstance := func(node *model.Node, pod bool) *model.Instance {
		instance := model.NewInstance("instance-1", model.NewApplication(model.NewApplicationId("cluster", "_", model.ApplicationKindExternalService, "app")))
		if node != nil {
			instance.Node = node
		}
		if pod {
			instance.Pod = &model.Pod{}
		}
		return instance
	}

	node := newNode("db1.example.com")
	agentless := newNode("")

	mongodb := &model.ApplicationInstrumentation{Type: model.ApplicationTypeMongodb}
	postgres := &model.ApplicationInstrumentation{Type: model.ApplicationTypePostgres}

	tests := []struct {
		name            string
		instrumentation *model.ApplicationInstrumentation
		instance        *model.Instance
		expected        string
	}{
		{"mongodb on a physical node with node-agent", mongodb, newInstance(node, false), "db1.example.com"},
		{"mongodb pod on kubernetes", mongodb, newInstance(node, true), ""},
		{"mongodb without a node", mongodb, newInstance(nil, false), ""},
		{"mongodb on a node without node-agent", mongodb, newInstance(agentless, false), ""},
		{"non-mongodb instrumentation", postgres, newInstance(node, false), ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, sniHostname(test.instrumentation, test.instance))
		})
	}
}

func TestApplicationInstrumentationJson(t *testing.T) {
	b, err := json.Marshal(ApplicationInstrumentation{
		Type: model.ApplicationTypeMongodb,
		Host: "10.0.0.1",
		Port: "27017",
		Sni:  "db1.example.com",
	})
	assert.NoError(t, err)
	assert.JSONEq(t, `{"type":"mongodb","host":"10.0.0.1","port":"27017","sni":"db1.example.com","credentials":{"username":"","password":""},"params":null,"instance":""}`, string(b))

	b, err = json.Marshal(ApplicationInstrumentation{
		Type: model.ApplicationTypeMongodb,
		Host: "10.0.0.1",
		Port: "27017",
	})
	assert.NoError(t, err)
	assert.NotContains(t, string(b), `"sni"`, "empty SNI must not be emitted")
}
