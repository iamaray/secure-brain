begin;

create extension if not exists pgcrypto;

create table if not exists public.app_meta (
    key text primary key,
    value text not null,
    updated_at timestamptz not null default now()
);

insert into public.app_meta (key, value)
values ('schema_version', '1')
on conflict (key) do update
set value = excluded.value,
    updated_at = now();

create table if not exists public.app_users (
    id uuid primary key default gen_random_uuid(),
    handle text not null unique,
    display_name text not null,
    created_at timestamptz not null default now(),
    constraint app_users_handle_format check (handle ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$')
);

create table if not exists public.brains (
    id uuid primary key default gen_random_uuid(),
    owner_user_id uuid not null references public.app_users(id) on delete restrict,
    slug text not null unique,
    canonical_id text generated always as ('brain.' || slug) stored,
    display_name text not null,
    status text not null default 'ready',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint brains_slug_format check (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    constraint brains_canonical_id_unique unique (canonical_id),
    constraint brains_status check (status in ('ready', 'disabled'))
);

create table if not exists public.services (
    id uuid primary key default gen_random_uuid(),
    owner_user_id uuid not null references public.app_users(id) on delete restrict,
    slug text not null unique,
    canonical_id text generated always as ('service.' || slug) stored,
    display_name text not null,
    status text not null default 'ready',
    capability_tags text[] not null default '{}',
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint services_slug_format check (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    constraint services_canonical_id_unique unique (canonical_id),
    constraint services_status check (status in ('ready', 'disabled'))
);

create table if not exists public.mock_sessions (
    id uuid primary key default gen_random_uuid(),
    token_hash bytea not null unique,
    user_id uuid not null references public.app_users(id) on delete cascade,
    created_at timestamptz not null default now(),
    last_seen_at timestamptz not null default now(),
    expires_at timestamptz not null,
    constraint mock_sessions_token_hash_length check (octet_length(token_hash) = 32),
    constraint mock_sessions_expiry check (expires_at > created_at)
);

create table if not exists public.assets (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    object_key text not null,
    storage_path text not null unique,
    original_filename text not null,
    media_type text not null default 'application/octet-stream',
    byte_size bigint not null default 0,
    sha256 text,
    format text not null default 'binary',
    processing_state text not null default 'uploading',
    parse_error text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint assets_object_key_unique unique (brain_id, object_key),
    constraint assets_object_key_nonempty check (length(object_key) between 1 and 512),
    constraint assets_byte_size check (byte_size >= 0 and byte_size <= 26214400),
    constraint assets_sha256 check (sha256 is null or sha256 ~ '^[0-9a-f]{64}$'),
    constraint assets_format check (format in ('text', 'markdown', 'csv', 'binary')),
    constraint assets_processing_state check (processing_state in ('uploading', 'ready', 'parse_failed', 'upload_failed'))
);

create table if not exists public.query_paths (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    path text not null,
    visibility text not null,
    state text not null default 'draft',
    operations text[] not null default '{}',
    config_version bigint not null default 1,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint query_paths_brain_path_unique unique (brain_id, path),
    constraint query_paths_path_format check (
        path ~ '^/[a-z0-9][a-z0-9/_-]*$'
        and path !~ '//'
        and right(path, 1) <> '/'
        and path !~ '(^|/)\.\.?(/|$)'
    ),
    constraint query_paths_visibility check (visibility in ('public', 'private')),
    constraint query_paths_state check (state in ('draft', 'enabled', 'disabled')),
    constraint query_paths_operations check (
        operations <@ array['raw_read', 'text_search', 'csv_query']::text[]
    ),
    constraint query_paths_config_version check (config_version > 0)
);

create table if not exists public.query_path_assets (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    asset_id uuid not null references public.assets(id) on delete cascade,
    position integer not null,
    primary key (query_path_id, asset_id),
    constraint query_path_assets_position_unique unique (query_path_id, position),
    constraint query_path_assets_position check (position >= 0)
);

create table if not exists public.query_path_brain_grants (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    brain_id uuid not null references public.brains(id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (query_path_id, brain_id)
);

create table if not exists public.query_path_service_grants (
    query_path_id uuid not null references public.query_paths(id) on delete cascade,
    service_id uuid not null references public.services(id) on delete cascade,
    created_at timestamptz not null default now(),
    primary key (query_path_id, service_id)
);

create table if not exists public.routes (
    id uuid primary key default gen_random_uuid(),
    query_path_id uuid not null unique references public.query_paths(id) on delete cascade,
    terminal_mode text not null,
    destination_brain_id uuid references public.brains(id) on delete restrict,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    constraint routes_terminal_mode check (terminal_mode in ('caller', 'fixed')),
    constraint routes_terminal_shape check (
        (terminal_mode = 'caller' and destination_brain_id is null)
        or
        (terminal_mode = 'fixed' and destination_brain_id is not null)
    )
);

create table if not exists public.route_hops (
    route_id uuid not null references public.routes(id) on delete cascade,
    hop_index integer not null,
    service_id uuid not null references public.services(id) on delete restrict,
    primary key (route_id, hop_index),
    constraint route_hops_index check (hop_index between 0 and 19)
);

create table if not exists public.route_executions (
    id uuid primary key default gen_random_uuid(),
    mode text not null,
    query_path_id uuid references public.query_paths(id) on delete set null,
    actor_user_id uuid references public.app_users(id) on delete set null,
    initiating_brain_id uuid references public.brains(id) on delete set null,
    source_brain_id uuid references public.brains(id) on delete set null,
    destination_brain_id uuid references public.brains(id) on delete set null,
    source_canonical_id text not null,
    source_path text not null,
    destination_canonical_id text,
    operation text not null,
    state text not null default 'created',
    route_snapshot jsonb not null default '{}'::jsonb,
    result_metadata jsonb not null default '{}'::jsonb,
    error_code text,
    error_message text,
    created_at timestamptz not null default now(),
    started_at timestamptz,
    completed_at timestamptz,
    constraint route_executions_mode check (mode in ('pull', 'push')),
    constraint route_executions_operation check (operation in ('raw_read', 'text_search', 'csv_query')),
    constraint route_executions_state check (state in ('created', 'authorizing', 'reading', 'processing', 'delivered', 'failed')),
    constraint route_executions_snapshot_object check (jsonb_typeof(route_snapshot) = 'object'),
    constraint route_executions_result_object check (jsonb_typeof(result_metadata) = 'object')
);

create table if not exists public.execution_hops (
    id uuid primary key default gen_random_uuid(),
    execution_id uuid not null references public.route_executions(id) on delete cascade,
    hop_index integer not null,
    service_id uuid references public.services(id) on delete set null,
    service_canonical_id text not null,
    status text not null,
    input_sha256 text not null,
    output_sha256 text not null,
    duration_ms integer not null,
    error_code text,
    created_at timestamptz not null default now(),
    constraint execution_hops_order_unique unique (execution_id, hop_index),
    constraint execution_hops_index check (hop_index between 0 and 19),
    constraint execution_hops_status check (status in ('completed', 'failed')),
    constraint execution_hops_input_sha check (input_sha256 ~ '^[0-9a-f]{64}$'),
    constraint execution_hops_output_sha check (output_sha256 ~ '^[0-9a-f]{64}$'),
    constraint execution_hops_duration check (duration_ms >= 0)
);

create table if not exists public.transfers (
    id uuid primary key default gen_random_uuid(),
    execution_id uuid not null unique references public.route_executions(id) on delete restrict,
    source_brain_id uuid references public.brains(id) on delete set null,
    destination_brain_id uuid references public.brains(id) on delete set null,
    source_canonical_id text not null,
    destination_canonical_id text not null,
    status text not null default 'pending',
    storage_path text not null unique,
    suggested_object_key text not null,
    suggested_filename text not null,
    media_type text not null,
    byte_size bigint not null,
    sha256 text not null,
    accepted_asset_id uuid unique references public.assets(id) on delete set null,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    resolved_at timestamptz,
    constraint transfers_status check (status in ('pending', 'accepted', 'rejected', 'expired')),
    constraint transfers_size check (byte_size >= 0 and byte_size <= 26214400),
    constraint transfers_sha check (sha256 ~ '^[0-9a-f]{64}$'),
    constraint transfers_expiry check (expires_at > created_at),
    constraint transfers_resolution_shape check (
        (status = 'pending' and resolved_at is null and accepted_asset_id is null)
        or (status = 'accepted' and resolved_at is not null and accepted_asset_id is not null)
        or (status in ('rejected', 'expired') and resolved_at is not null and accepted_asset_id is null)
    )
);

create table if not exists public.chat_messages (
    id uuid primary key default gen_random_uuid(),
    brain_id uuid not null references public.brains(id) on delete cascade,
    user_id uuid not null references public.app_users(id) on delete cascade,
    role text not null,
    content text not null,
    model text,
    created_at timestamptz not null default now(),
    constraint chat_messages_role check (role in ('user', 'assistant')),
    constraint chat_messages_content_length check (length(content) between 1 and 20000),
    constraint chat_messages_model_shape check (
        (role = 'user' and model is null) or (role = 'assistant' and model is not null)
    )
);

create table if not exists public.audit_events (
    id uuid primary key default gen_random_uuid(),
    event_type text not null,
    actor_user_id uuid references public.app_users(id) on delete set null,
    resource_type text not null,
    resource_id uuid,
    brain_id uuid references public.brains(id) on delete set null,
    service_id uuid references public.services(id) on delete set null,
    execution_id uuid references public.route_executions(id) on delete set null,
    status text not null,
    metadata jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    constraint audit_events_status check (status in ('allowed', 'denied', 'succeeded', 'failed', 'pending')),
    constraint audit_events_metadata_object check (jsonb_typeof(metadata) = 'object')
);

create table if not exists public.audit_event_viewers (
    audit_event_id uuid not null references public.audit_events(id) on delete cascade,
    user_id uuid not null references public.app_users(id) on delete cascade,
    primary key (audit_event_id, user_id)
);

create table if not exists public.idempotency_records (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references public.app_users(id) on delete cascade,
    scope text not null,
    idempotency_key text not null,
    request_hash text not null,
    response_status integer,
    response_body jsonb,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null,
    constraint idempotency_records_unique unique (user_id, scope, idempotency_key),
    constraint idempotency_records_key_length check (length(idempotency_key) between 8 and 200),
    constraint idempotency_records_hash check (request_hash ~ '^[0-9a-f]{64}$'),
    constraint idempotency_records_response_status check (response_status is null or response_status between 200 and 599),
    constraint idempotency_records_response_shape check (response_body is null or jsonb_typeof(response_body) = 'object'),
    constraint idempotency_records_expiry check (expires_at > created_at)
);

create index if not exists brains_owner_idx on public.brains (owner_user_id, created_at desc);
create index if not exists services_owner_idx on public.services (owner_user_id, created_at desc);
create index if not exists mock_sessions_user_idx on public.mock_sessions (user_id, expires_at desc);
create index if not exists mock_sessions_expiry_idx on public.mock_sessions (expires_at);
create index if not exists assets_brain_idx on public.assets (brain_id, created_at desc);
create index if not exists assets_state_idx on public.assets (processing_state);
create index if not exists query_paths_brain_state_idx on public.query_paths (brain_id, state, updated_at desc);
create index if not exists query_path_assets_asset_idx on public.query_path_assets (asset_id);
create index if not exists query_path_brain_grants_brain_idx on public.query_path_brain_grants (brain_id);
create index if not exists query_path_service_grants_service_idx on public.query_path_service_grants (service_id);
create index if not exists route_hops_service_idx on public.route_hops (service_id);
create index if not exists route_executions_actor_idx on public.route_executions (actor_user_id, created_at desc);
create index if not exists route_executions_source_idx on public.route_executions (source_brain_id, created_at desc);
create index if not exists route_executions_destination_idx on public.route_executions (destination_brain_id, created_at desc);
create index if not exists route_executions_state_idx on public.route_executions (state, created_at desc);
create index if not exists transfers_source_idx on public.transfers (source_brain_id, created_at desc);
create index if not exists transfers_destination_idx on public.transfers (destination_brain_id, status, created_at desc);
create index if not exists transfers_expiry_idx on public.transfers (expires_at) where status = 'pending';
create index if not exists chat_messages_brain_idx on public.chat_messages (brain_id, created_at desc);
create index if not exists audit_events_created_idx on public.audit_events (created_at desc);
create index if not exists audit_events_resource_idx on public.audit_events (resource_type, resource_id, created_at desc);
create index if not exists audit_events_execution_idx on public.audit_events (execution_id, created_at);
create index if not exists audit_event_viewers_user_idx on public.audit_event_viewers (user_id, audit_event_id);
create index if not exists idempotency_records_expiry_idx on public.idempotency_records (expires_at);

create or replace function public.set_updated_at()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    new.updated_at = now();
    return new;
end;
$$;

drop trigger if exists brains_set_updated_at on public.brains;
create trigger brains_set_updated_at
before update on public.brains
for each row execute function public.set_updated_at();

drop trigger if exists services_set_updated_at on public.services;
create trigger services_set_updated_at
before update on public.services
for each row execute function public.set_updated_at();

drop trigger if exists assets_set_updated_at on public.assets;
create trigger assets_set_updated_at
before update on public.assets
for each row execute function public.set_updated_at();

drop trigger if exists query_paths_set_updated_at on public.query_paths;
create trigger query_paths_set_updated_at
before update on public.query_paths
for each row execute function public.set_updated_at();

drop trigger if exists routes_set_updated_at on public.routes;
create trigger routes_set_updated_at
before update on public.routes
for each row execute function public.set_updated_at();

create or replace function public.block_asset_delete_when_enabled()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.query_path_assets qpa
        join public.query_paths qp on qp.id = qpa.query_path_id
        where qpa.asset_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: asset is referenced by an enabled query path';
    end if;
    return old;
end;
$$;

drop trigger if exists assets_block_enabled_reference on public.assets;
create trigger assets_block_enabled_reference
before delete on public.assets
for each row execute function public.block_asset_delete_when_enabled();

create or replace function public.block_brain_delete_when_active_route()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.query_paths qp
        where qp.brain_id = old.id
          and qp.state = 'enabled'
    ) or exists (
        select 1
        from public.routes r
        join public.query_paths qp on qp.id = r.query_path_id
        where r.destination_brain_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: brain is referenced by an active route';
    end if;
    return old;
end;
$$;

drop trigger if exists brains_block_active_route on public.brains;
create trigger brains_block_active_route
before delete on public.brains
for each row execute function public.block_brain_delete_when_active_route();

create or replace function public.block_service_delete_when_active_route()
returns trigger
language plpgsql
set search_path = public
as $$
begin
    if exists (
        select 1
        from public.route_hops rh
        join public.routes r on r.id = rh.route_id
        join public.query_paths qp on qp.id = r.query_path_id
        where rh.service_id = old.id
          and qp.state = 'enabled'
    ) then
        raise exception using
            errcode = 'P0001',
            message = 'RESOURCE_IN_USE: service is referenced by an active route';
    end if;
    return old;
end;
$$;

drop trigger if exists services_block_active_route on public.services;
create trigger services_block_active_route
before delete on public.services
for each row execute function public.block_service_delete_when_active_route();

alter table public.app_meta enable row level security;
alter table public.app_users enable row level security;
alter table public.brains enable row level security;
alter table public.services enable row level security;
alter table public.mock_sessions enable row level security;
alter table public.assets enable row level security;
alter table public.query_paths enable row level security;
alter table public.query_path_assets enable row level security;
alter table public.query_path_brain_grants enable row level security;
alter table public.query_path_service_grants enable row level security;
alter table public.routes enable row level security;
alter table public.route_hops enable row level security;
alter table public.route_executions enable row level security;
alter table public.execution_hops enable row level security;
alter table public.transfers enable row level security;
alter table public.chat_messages enable row level security;
alter table public.audit_events enable row level security;
alter table public.audit_event_viewers enable row level security;
alter table public.idempotency_records enable row level security;

revoke all on all tables in schema public from anon, authenticated;
revoke all on all sequences in schema public from anon, authenticated;

insert into storage.buckets (id, name, public, file_size_limit, allowed_mime_types)
values ('securebrain-private', 'securebrain-private', false, 26214400, null)
on conflict (id) do update
set name = excluded.name,
    public = excluded.public,
    file_size_limit = excluded.file_size_limit,
    allowed_mime_types = excluded.allowed_mime_types;

insert into public.app_users (id, handle, display_name)
values
    ('00000000-0000-4000-8000-000000000001', 'maya', 'Maya'),
    ('00000000-0000-4000-8000-000000000002', 'anish', 'Anish'),
    ('00000000-0000-4000-8000-000000000003', 'atlas', 'Atlas')
on conflict (id) do update
set handle = excluded.handle,
    display_name = excluded.display_name;

insert into public.brains (id, owner_user_id, slug, display_name)
values
    ('10000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001', 'maya', 'Maya Brain'),
    ('10000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002', 'anish', 'Anish Brain'),
    ('10000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000003', 'atlas', 'Atlas Brain')
on conflict (id) do update
set owner_user_id = excluded.owner_user_id,
    slug = excluded.slug,
    display_name = excluded.display_name;

insert into public.services (id, owner_user_id, slug, display_name, capability_tags)
values
    ('20000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001', 'notion', 'Notion', array['HTTP', 'Files']),
    ('20000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002', 'obsidian', 'Obsidian', array['MCP', 'Files']),
    ('20000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000001', 'de-identification', 'De-identification', array['Privacy', 'Identity']),
    ('20000000-0000-4000-8000-000000000004', '00000000-0000-4000-8000-000000000001', 'pii-scan', 'PII scan', array['Privacy', 'Identity']),
    ('20000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000002', 'entity-extraction', 'Entity extraction', array['Analysis', 'Identity']),
    ('20000000-0000-4000-8000-000000000006', '00000000-0000-4000-8000-000000000003', 'policy-check', 'Policy check', array['Policy', 'Identity']),
    ('20000000-0000-4000-8000-000000000007', '00000000-0000-4000-8000-000000000001', 'redaction', 'Redaction', array['Privacy', 'Identity']),
    ('20000000-0000-4000-8000-000000000008', '00000000-0000-4000-8000-000000000002', 'summarization', 'Summarization', array['Analysis', 'Identity']),
    ('20000000-0000-4000-8000-000000000009', '00000000-0000-4000-8000-000000000003', 'classification', 'Classification', array['Analysis', 'Identity']),
    ('20000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000002', 'deduplication', 'Deduplication', array['Data hygiene', 'Identity'])
on conflict (slug) do update
set owner_user_id = excluded.owner_user_id,
    display_name = excluded.display_name,
    capability_tags = excluded.capability_tags;

commit;
