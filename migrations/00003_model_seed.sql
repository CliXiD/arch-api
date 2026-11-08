-- Built-in vocabulary and environments, in every environment (prod included). No teams.
-- +goose Up
INSERT INTO environments (id, name, sort) VALUES
    ('dev', 'dev', 1),
    ('uat', 'uat', 2),
    ('prod', 'prod', 3);

INSERT INTO node_types (id, kind, name, builtin) VALUES
    ('service', 'application', 'Service', true),
    ('k8s-cluster', 'infrastructure', 'Kubernetes cluster', true),
    ('database', 'infrastructure', 'Database', true),
    ('queue', 'infrastructure', 'Queue', true),
    ('bucket', 'infrastructure', 'Bucket', true),
    ('vpc', 'network', 'VPC', true),
    ('landing-zone', 'network', 'Landing zone', true),
    ('transit-gateway', 'network', 'Transit gateway', true),
    ('vpn', 'network', 'VPN', true),
    ('direct-connect', 'network', 'Direct Connect', true),
    ('firewall', 'network', 'Firewall', true),
    ('on-prem-system', 'external', 'On-prem system', true),
    ('saas', 'external', 'SaaS', true),
    ('team', 'team', 'Team', true);

INSERT INTO edge_types (id, name, attributes_schema, allowed, builtin) VALUES
    ('depends_on', 'Depends on', '{"type":"object","properties":{}}',
        '[{"from":"application","to":"application"},{"from":"application","to":"infrastructure"},{"from":"application","to":"external"}]', true),
    ('calls', 'Calls',
        '{"type":"object","properties":{"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","other"]},"port":{"type":"number","minimum":1,"maximum":65535}}}',
        '[{"from":"application","to":"application"},{"from":"application","to":"external"}]', true),
    ('runs_on', 'Runs on', '{"type":"object","properties":{}}',
        '[{"from":"application","to":"infrastructure"}]', true),
    ('stores_data_in', 'Stores data in', '{"type":"object","properties":{}}',
        '[{"from":"application","to":"infrastructure"}]', true),
    ('routes_via', 'Routes via', '{"type":"object","properties":{}}',
        '[{"from":"application","to":"network"},{"from":"network","to":"network"},{"from":"network","to":"application"},{"from":"network","to":"external"},{"from":"infrastructure","to":"network"}]', true),
    ('owned_by', 'Owned by', '{"type":"object","properties":{}}',
        '[{"from":"application","to":"team"},{"from":"infrastructure","to":"team"},{"from":"network","to":"team"},{"from":"external","to":"team"}]', true);

-- +goose Down
DELETE FROM edge_types WHERE id IN ('depends_on', 'calls', 'runs_on', 'stores_data_in', 'routes_via', 'owned_by');
DELETE FROM node_types WHERE id IN (
    'service', 'k8s-cluster', 'database', 'queue', 'bucket', 'vpc', 'landing-zone', 'transit-gateway',
    'vpn', 'direct-connect', 'firewall', 'on-prem-system', 'saas', 'team'
);
DELETE FROM environments WHERE id IN ('dev', 'uat', 'prod');
