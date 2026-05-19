"""
groups/appsync.py — AppSync compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _appsync(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("appsync")


# ── appsync-apis ──────────────────────────────────────────────────────────────

def CreateGraphqlApi(ctx: TestContext) -> None:
    api_name = f"compat-{ctx.run_id}"
    resp = _appsync(ctx).create_graphql_api(
        name=api_name,
        authenticationType="API_KEY",
    )
    api = resp.get("graphqlApi", {})
    if not api.get("apiId"):
        raise AssertionError("CreateGraphqlApi: missing apiId")
    ctx["appsync_api_id"] = api["apiId"]


def GetGraphqlApi(ctx: TestContext) -> None:
    api_id = ctx.get("appsync_api_id")
    if not api_id:
        raise AssertionError("GetGraphqlApi: no api from CreateGraphqlApi")
    resp = _appsync(ctx).get_graphql_api(apiId=api_id)
    if not resp.get("graphqlApi", {}).get("apiId"):
        raise AssertionError("GetGraphqlApi: missing apiId")


def ListGraphqlApis(ctx: TestContext) -> None:
    _appsync(ctx).list_graphql_apis()


def DeleteGraphqlApi(ctx: TestContext) -> None:
    api_id = ctx.get("appsync_api_id")
    if not api_id:
        return
    _appsync(ctx).delete_graphql_api(apiId=api_id)


def UpdateGraphqlApi(ctx: TestContext) -> None:
    api_id = ctx.get("appsync_api_id")
    if not api_id:
        raise AssertionError("UpdateGraphqlApi: no api_id")
    resp = _appsync(ctx).update_graphql_api(
        apiId=api_id,
        name=f"compat-updated-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    if not resp.get("graphqlApi"):
        raise AssertionError("UpdateGraphqlApi: missing graphqlApi")


# ── appsync-functions ─────────────────────────────────────────────────────────

def CreateFunction(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    if not api_id:
        raise AssertionError("CreateFunction: no api from setup")
    resp = _appsync(ctx).create_function(
        apiId=api_id,
        name="CompatFn",
        dataSourceName="FnNoneDS",
        requestMappingTemplate='{"version":"2018-05-29","payload":{}}',
        responseMappingTemplate="$util.toJson($context.result)",
    )
    fn = resp.get("functionConfiguration", {})
    fn_id = fn.get("functionId")
    if not fn_id:
        raise AssertionError("CreateFunction: missing functionId")
    ctx["fn_id"] = fn_id


def GetFunction(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    fn_id = ctx.get("fn_id")
    if not api_id or not fn_id:
        raise AssertionError("GetFunction: missing api or function id from setup")
    resp = _appsync(ctx).get_function(apiId=api_id, functionId=fn_id)
    if not resp.get("functionConfiguration", {}).get("name"):
        raise AssertionError("GetFunction: missing name")


def UpdateFunction(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    fn_id = ctx.get("fn_id")
    if not api_id or not fn_id:
        raise AssertionError("UpdateFunction: missing api or function id")
    _appsync(ctx).update_function(
        apiId=api_id,
        functionId=fn_id,
        name="CompatFnUpdated",
        dataSourceName="FnNoneDS",
        requestMappingTemplate='{"version":"2018-05-29","payload":{}}',
        responseMappingTemplate="$util.toJson($context.result)",
    )


def ListFunctions(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    if not api_id:
        raise AssertionError("ListFunctions: no api from setup")
    _appsync(ctx).list_functions(apiId=api_id)


def DeleteFunction(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    if not api_id:
        raise AssertionError("DeleteFunction: no api from setup")
    cr = _appsync(ctx).create_function(
        apiId=api_id,
        name="CompatFnDel",
        dataSourceName="FnNoneDS",
        requestMappingTemplate="{}",
        responseMappingTemplate="{}",
    )
    del_id = cr.get("functionConfiguration", {}).get("functionId")
    _appsync(ctx).delete_function(apiId=api_id, functionId=del_id)


# ── appsync-tags ──────────────────────────────────────────────────────────────

def TagResource(ctx: TestContext) -> None:
    arn = ctx.get("tag_api_arn")
    if not arn:
        raise AssertionError("TagResource: no API ARN from setup")
    _appsync(ctx).tag_resource(resourceArn=arn, tags={"env": "compat"})


def ListTagsForResource(ctx: TestContext) -> None:
    arn = ctx.get("tag_api_arn")
    if not arn:
        raise AssertionError("ListTagsForResource: no API ARN from setup")
    resp = _appsync(ctx).list_tags_for_resource(resourceArn=arn)
    tags = resp.get("tags", {})
    if tags.get("env") != "compat":
        raise AssertionError(f"ListTagsForResource: expected env=compat, got {tags}")


def UntagResource(ctx: TestContext) -> None:
    arn = ctx.get("tag_api_arn")
    if not arn:
        raise AssertionError("UntagResource: no API ARN from setup")
    _appsync(ctx).untag_resource(resourceArn=arn, tagKeys=["env"])


# ── appsync-schemas ───────────────────────────────────────────────────────────

def StartSchemaCreation(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).start_schema_creation(apiId=api_id, definition=b"type Query { hello: String }")
    if not resp.get("status"):
        raise AssertionError("StartSchemaCreation: missing status")


def GetSchemaCreationStatus(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).get_schema_creation_status(apiId=api_id)
    if not resp.get("status"):
        raise AssertionError("GetSchemaCreationStatus: missing status")


# ── appsync-api-keys ─────────────────────────────────────────────────────────

def CreateApiKey(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).create_api_key(apiId=api_id, description="compat test key")
    key = resp.get("apiKey", {})
    key_id = key.get("id")
    if not key_id:
        raise AssertionError("CreateApiKey: missing id")
    ctx["key_id"] = key_id


def ListApiKeys(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).list_api_keys(apiId=api_id)


def UpdateApiKey(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    key_id = ctx.get("key_id")
    _appsync(ctx).update_api_key(apiId=api_id, id=key_id, description="updated compat key")


def DeleteApiKey(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    cr = _appsync(ctx).create_api_key(apiId=api_id)
    del_id = cr.get("apiKey", {}).get("id")
    _appsync(ctx).delete_api_key(apiId=api_id, id=del_id)


# ── appsync-datasources ──────────────────────────────────────────────────────

def CreateDataSource(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).create_data_source(apiId=api_id, name="CompatNoneDS", type="NONE")
    ds = resp.get("dataSource", {})
    if ds.get("name") != "CompatNoneDS":
        raise AssertionError("CreateDataSource: name mismatch")


def GetDataSource(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).get_data_source(apiId=api_id, name="CompatNoneDS")
    ds = resp.get("dataSource", {})
    if not ds.get("dataSourceArn"):
        raise AssertionError("GetDataSource: missing ARN")


def UpdateDataSource(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).update_data_source(apiId=api_id, name="CompatNoneDS", type="NONE", description="updated")


def ListDataSources(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).list_data_sources(apiId=api_id)


def DeleteDataSource(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).create_data_source(apiId=api_id, name="CompatDelDS", type="NONE")
    _appsync(ctx).delete_data_source(apiId=api_id, name="CompatDelDS")


# ── appsync-resolvers ────────────────────────────────────────────────────────

def CreateResolver(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).create_resolver(
        apiId=api_id,
        typeName="Query",
        fieldName="hello",
        dataSourceName="ResNoneDS",
        kind="UNIT",
        requestMappingTemplate='{"version":"2018-05-29","payload":"world"}',
        responseMappingTemplate="$util.toJson($context.result)",
    )
    r = resp.get("resolver", {})
    if not r.get("fieldName"):
        raise AssertionError("CreateResolver: missing fieldName")


def GetResolver(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).get_resolver(apiId=api_id, typeName="Query", fieldName="hello")
    r = resp.get("resolver", {})
    if not r.get("resolverArn"):
        raise AssertionError("GetResolver: missing resolverArn")


def UpdateResolver(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).update_resolver(
        apiId=api_id,
        typeName="Query",
        fieldName="hello",
        dataSourceName="ResNoneDS",
        kind="UNIT",
        requestMappingTemplate='{"version":"2018-05-29","payload":"updated"}',
        responseMappingTemplate="$util.toJson($context.result)",
    )


def ListResolvers(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).list_resolvers(apiId=api_id, typeName="Query")


def ListResolversByFunction(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    fn_id = ctx.get("fn_id")
    _appsync(ctx).create_resolver(
        apiId=api_id,
        typeName="Query",
        fieldName="goodbye",
        kind="PIPELINE",
        pipelineConfig={"functions": [fn_id]},
        requestMappingTemplate="{}",
        responseMappingTemplate="$util.toJson($context.result)",
    )
    resp = _appsync(ctx).list_resolvers_by_function(apiId=api_id, functionId=fn_id)
    resolvers = resp.get("resolvers", [])
    if len(resolvers) == 0:
        raise AssertionError("ListResolversByFunction: expected at least one resolver")


def DeleteResolver(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).create_resolver(
        apiId=api_id,
        typeName="Query",
        fieldName="extra",
        dataSourceName="ResNoneDS",
        kind="UNIT",
    )
    _appsync(ctx).delete_resolver(apiId=api_id, typeName="Query", fieldName="extra")


# ── appsync-types ─────────────────────────────────────────────────────────────

def CreateType(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).create_type(
        apiId=api_id,
        definition="type CompatType { id: ID, name: String }",
        format="SDL",
    )
    tp = resp.get("type", {})
    if not tp.get("name"):
        raise AssertionError("CreateType: missing name")


def GetType(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).get_type(apiId=api_id, typeName="CompatType", format="SDL")


def UpdateType(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).update_type(
        apiId=api_id,
        typeName="CompatType",
        definition="type CompatType { id: ID, name: String, age: Int }",
        format="SDL",
    )


def ListTypes(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).list_types(apiId=api_id, format="SDL")


def DeleteType(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).create_type(
        apiId=api_id,
        definition="type CompatDelType { x: String }",
        format="SDL",
    )
    _appsync(ctx).delete_type(apiId=api_id, typeName="CompatDelType")


# ── appsync-env-vars ─────────────────────────────────────────────────────────

def PutGraphqlApiEnvironmentVariables(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).put_graphql_api_environment_variables(
        apiId=api_id,
        environmentVariables={"DB_HOST": "localhost", "DB_PORT": "5432"},
    )


def GetGraphqlApiEnvironmentVariables(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).get_graphql_api_environment_variables(apiId=api_id)
    env = resp.get("environmentVariables", {})
    if env.get("DB_HOST") != "localhost":
        raise AssertionError("GetGraphqlApiEnvironmentVariables: value mismatch")


# ── appsync-domains ──────────────────────────────────────────────────────────

def CreateDomainName(ctx: TestContext) -> None:
    dn = f"{ctx.run_id}.example.com"
    resp = _appsync(ctx).create_domain_name(
        domainName=dn,
        certificateArn="arn:aws:acm:us-east-1:000000000000:certificate/test",
    )
    cfg = resp.get("domainNameConfig", {})
    if not cfg.get("domainName"):
        raise AssertionError("CreateDomainName: missing domainName")
    ctx["domain_name"] = dn


def GetDomainName(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    _appsync(ctx).get_domain_name(domainName=dn)


def UpdateDomainName(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    _appsync(ctx).update_domain_name(domainName=dn, description="updated-domain")


def ListDomainNames(ctx: TestContext) -> None:
    _appsync(ctx).list_domain_names()


def AssociateApi(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).associate_api(domainName=dn, apiId=api_id)
    if not resp.get("apiAssociation"):
        raise AssertionError("AssociateApi: missing apiAssociation")


def GetApiAssociation(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    resp = _appsync(ctx).get_api_association(domainName=dn)
    if not resp.get("apiAssociation"):
        raise AssertionError("GetApiAssociation: missing apiAssociation")


def DisassociateApi(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    _appsync(ctx).disassociate_api(domainName=dn)


def DeleteDomainName(ctx: TestContext) -> None:
    dn = f"del-{ctx.run_id}.example.com"
    _appsync(ctx).create_domain_name(
        domainName=dn,
        certificateArn="arn:aws:acm:us-east-1:000000000000:certificate/test",
    )
    _appsync(ctx).delete_domain_name(domainName=dn)


# ── appsync-cache ─────────────────────────────────────────────────────────────

def CreateApiCache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    resp = _appsync(ctx).create_api_cache(
        apiId=api_id,
        type="T2_SMALL",
        apiCachingBehavior="FULL_REQUEST_CACHING",
        ttl=300,
    )
    if not resp.get("apiCache"):
        raise AssertionError("CreateApiCache: missing apiCache")


def GetApiCache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).get_api_cache(apiId=api_id)


def UpdateApiCache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).update_api_cache(
        apiId=api_id,
        type="T2_MEDIUM",
        apiCachingBehavior="FULL_REQUEST_CACHING",
        ttl=600,
    )


def FlushApiCache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).flush_api_cache(apiId=api_id)


def DeleteApiCache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    _appsync(ctx).delete_api_cache(apiId=api_id)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    # appsync-apis
    "CreateGraphqlApi": CreateGraphqlApi,
    "GetGraphqlApi": GetGraphqlApi,
    "UpdateGraphqlApi": UpdateGraphqlApi,
    "ListGraphqlApis": ListGraphqlApis,
    "DeleteGraphqlApi": DeleteGraphqlApi,
    # appsync-schemas
    "StartSchemaCreation": StartSchemaCreation,
    "GetSchemaCreationStatus": GetSchemaCreationStatus,
    # appsync-api-keys
    "CreateApiKey": CreateApiKey,
    "ListApiKeys": ListApiKeys,
    "UpdateApiKey": UpdateApiKey,
    "DeleteApiKey": DeleteApiKey,
    # appsync-datasources
    "CreateDataSource": CreateDataSource,
    "GetDataSource": GetDataSource,
    "UpdateDataSource": UpdateDataSource,
    "ListDataSources": ListDataSources,
    "DeleteDataSource": DeleteDataSource,
    # appsync-functions — group-qualified to avoid overriding lambda group names
    "appsync-functions:CreateFunction": CreateFunction,
    "appsync-functions:GetFunction": GetFunction,
    "appsync-functions:UpdateFunction": UpdateFunction,
    "appsync-functions:ListFunctions": ListFunctions,
    "appsync-functions:DeleteFunction": DeleteFunction,
    # appsync-resolvers
    "CreateResolver": CreateResolver,
    "GetResolver": GetResolver,
    "UpdateResolver": UpdateResolver,
    "ListResolvers": ListResolvers,
    "ListResolversByFunction": ListResolversByFunction,
    "DeleteResolver": DeleteResolver,
    # appsync-types
    "CreateType": CreateType,
    "GetType": GetType,
    "UpdateType": UpdateType,
    "ListTypes": ListTypes,
    "DeleteType": DeleteType,
    # appsync-tags — group-qualified to avoid overriding secretsmanager names
    "appsync-tags:TagResource": TagResource,
    "appsync-tags:ListTagsForResource": ListTagsForResource,
    "appsync-tags:UntagResource": UntagResource,
    # appsync-env-vars
    "PutGraphqlApiEnvironmentVariables": PutGraphqlApiEnvironmentVariables,
    "GetGraphqlApiEnvironmentVariables": GetGraphqlApiEnvironmentVariables,
    # appsync-domains
    "CreateDomainName": CreateDomainName,
    "GetDomainName": GetDomainName,
    "UpdateDomainName": UpdateDomainName,
    "ListDomainNames": ListDomainNames,
    "AssociateApi": AssociateApi,
    "GetApiAssociation": GetApiAssociation,
    "DisassociateApi": DisassociateApi,
    "DeleteDomainName": DeleteDomainName,
    # appsync-cache
    "CreateApiCache": CreateApiCache,
    "GetApiCache": GetApiCache,
    "UpdateApiCache": UpdateApiCache,
    "FlushApiCache": FlushApiCache,
    "DeleteApiCache": DeleteApiCache,
}

SETUP = {
    "appsync-schemas": lambda ctx: _setup_generic(ctx),
    "appsync-api-keys": lambda ctx: _setup_generic(ctx),
    "appsync-datasources": lambda ctx: _setup_generic(ctx),
    "appsync-functions": lambda ctx: _setup_functions(ctx),
    "appsync-resolvers": lambda ctx: _setup_resolvers(ctx),
    "appsync-types": lambda ctx: _setup_types(ctx),
    "appsync-tags": lambda ctx: _setup_tags(ctx),
    "appsync-env-vars": lambda ctx: _setup_generic(ctx),
    "appsync-domains": lambda ctx: _setup_generic(ctx),
    "appsync-cache": lambda ctx: _setup_generic(ctx),
}
TEARDOWN = {
    "appsync-apis": lambda ctx: _teardown_appsync_api(ctx),
    "appsync-schemas": lambda ctx: _teardown_generic(ctx),
    "appsync-api-keys": lambda ctx: _teardown_generic(ctx),
    "appsync-datasources": lambda ctx: _teardown_generic(ctx),
    "appsync-functions": lambda ctx: _teardown_functions(ctx),
    "appsync-resolvers": lambda ctx: _teardown_generic(ctx),
    "appsync-types": lambda ctx: _teardown_generic(ctx),
    "appsync-tags": lambda ctx: _teardown_tags(ctx),
    "appsync-env-vars": lambda ctx: _teardown_generic(ctx),
    "appsync-domains": lambda ctx: _teardown_domains(ctx),
    "appsync-cache": lambda ctx: _teardown_cache(ctx),
}


def _teardown_appsync_api(ctx: TestContext) -> None:
    api_id = ctx.get("appsync_api_id")
    if api_id:
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass


def _setup_functions(ctx: TestContext) -> None:
    resp = _appsync(ctx).create_graphql_api(
        name=f"compat-fn-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    api_id = resp.get("graphqlApi", {}).get("apiId")
    ctx["fn_api_id"] = api_id
    _appsync(ctx).create_data_source(
        apiId=api_id,
        name="FnNoneDS",
        type="NONE",
    )


def _teardown_functions(ctx: TestContext) -> None:
    api_id = ctx.get("fn_api_id")
    if api_id:
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass


def _setup_tags(ctx: TestContext) -> None:
    resp = _appsync(ctx).create_graphql_api(
        name=f"compat-tag-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    api = resp.get("graphqlApi", {})
    ctx["tag_api_id"] = api.get("apiId")
    ctx["tag_api_arn"] = api.get("arn")


def _teardown_tags(ctx: TestContext) -> None:
    api_id = ctx.get("tag_api_id")
    if api_id:
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass


def _setup_generic(ctx: TestContext) -> None:
    resp = _appsync(ctx).create_graphql_api(
        name=f"compat-grp-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    api = resp.get("graphqlApi", {})
    ctx["api_id"] = api.get("apiId")
    ctx["api_arn"] = api.get("arn")


def _teardown_generic(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    if api_id:
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass


def _setup_resolvers(ctx: TestContext) -> None:
    resp = _appsync(ctx).create_graphql_api(
        name=f"compat-res-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    api_id = resp.get("graphqlApi", {}).get("apiId")
    ctx["api_id"] = api_id
    _appsync(ctx).start_schema_creation(apiId=api_id, definition=b"type Query { hello: String, goodbye: String, extra: String }")
    _appsync(ctx).create_data_source(apiId=api_id, name="ResNoneDS", type="NONE")
    fn_resp = _appsync(ctx).create_function(
        apiId=api_id,
        name="ResFn",
        dataSourceName="ResNoneDS",
        requestMappingTemplate="{}",
        responseMappingTemplate="{}",
    )
    ctx["fn_id"] = fn_resp.get("functionConfiguration", {}).get("functionId")


def _setup_types(ctx: TestContext) -> None:
    resp = _appsync(ctx).create_graphql_api(
        name=f"compat-types-{ctx.run_id}",
        authenticationType="API_KEY",
    )
    api_id = resp.get("graphqlApi", {}).get("apiId")
    ctx["api_id"] = api_id
    _appsync(ctx).start_schema_creation(apiId=api_id, definition=b"type Query { hello: String }")


def _teardown_domains(ctx: TestContext) -> None:
    dn = ctx.get("domain_name")
    if dn:
        try:
            _appsync(ctx).disassociate_api(domainName=dn)
        except Exception:
            pass
        try:
            _appsync(ctx).delete_domain_name(domainName=dn)
        except Exception:
            pass
    api_id = ctx.get("api_id")
    if api_id:
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass


def _teardown_cache(ctx: TestContext) -> None:
    api_id = ctx.get("api_id")
    if api_id:
        try:
            _appsync(ctx).delete_api_cache(apiId=api_id)
        except Exception:
            pass
        try:
            _appsync(ctx).delete_graphql_api(apiId=api_id)
        except Exception:
            pass

