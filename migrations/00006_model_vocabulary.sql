-- Wider built-in vocabulary (hub issue #17): four node types, external callers, calls and stores_data_in attributes.
-- +goose Up
INSERT INTO node_types (id, kind, name, builtin) VALUES
    ('client', 'external', 'Client or actor', true),
    ('load-balancer', 'network', 'Load balancer / ingress', true),
    ('container-registry', 'infrastructure', 'Container registry', true),
    ('job', 'application', 'Job', true);

UPDATE edge_types SET allowed = allowed || '[{"from":"external","to":"application"}]'::jsonb
    WHERE id IN ('calls', 'depends_on');

UPDATE edge_types SET attributes_schema =
    '{"type":"object","properties":{"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","postgres","otlp","other"]},"port":{"type":"number","minimum":1,"maximum":65535},"path":{"type":"string"}}}'
    WHERE id = 'calls';

UPDATE edge_types SET attributes_schema =
    '{"type":"object","properties":{"port":{"type":"number","minimum":1,"maximum":65535},"role":{"type":"string"}}}'
    WHERE id = 'stores_data_in';

-- +goose Down
UPDATE edge_types SET
    attributes_schema = '{"type":"object","properties":{}}',
    allowed = '[{"from":"application","to":"infrastructure"}]'
    WHERE id = 'stores_data_in';

UPDATE edge_types SET
    attributes_schema = '{"type":"object","properties":{"protocol":{"type":"string","enum":["http","https","grpc","tcp","amqp","kafka","other"]},"port":{"type":"number","minimum":1,"maximum":65535}}}',
    allowed = '[{"from":"application","to":"application"},{"from":"application","to":"external"}]'
    WHERE id = 'calls';

UPDATE edge_types SET
    attributes_schema = '{"type":"object","properties":{}}',
    allowed = '[{"from":"application","to":"application"},{"from":"application","to":"infrastructure"},{"from":"application","to":"external"}]'
    WHERE id = 'depends_on';

DELETE FROM node_types WHERE id IN ('client', 'load-balancer', 'container-registry', 'job');
