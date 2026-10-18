"""Read-only parity check for the three full-workflow HTTP registrars.

This is deliberately a bounded source reader, not a Go/TypeScript compiler.
Unsupported registrations or URL expressions fail visibly instead of disappearing
from coverage. Examples/JSON Schema validation remain validate_artifacts.py's job.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
from urllib.parse import urlsplit

ROUTE_FILES = tuple("internal/lingdoc/workspace/" + name for name in
                    ("g2_full_http.go", "g3_g6_full_http.go", "full_workflow_http.go"))
CLIENT_FILES = tuple("frontend/src/api/lingdoc/" + name for name in
                     ("g2.ts", "mainlineChanges.ts", "collaboration.ts", "chapterReview.ts"))
METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}
TICK = chr(96)


class ContractParityError(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise ContractParityError(message)


def close_at(text, start, opening="(", closing=")"):
    """Match source delimiters while respecting strings and source comments."""
    depth, i = 1, start + 1
    while i < len(text):
        char = text[i]
        if char in ("'", '"', TICK):
            quote, i = char, i + 1
            while i < len(text):
                if text[i] == "\\" and quote != TICK:
                    i += 2
                elif text[i] == quote:
                    i += 1
                    break
                else:
                    i += 1
            continue
        if text.startswith("//", i):
            end = text.find("\n", i)
            i = len(text) if end < 0 else end + 1
            continue
        if text.startswith("/*", i):
            end = text.find("*/", i + 2)
            require(end >= 0, "unterminated source comment")
            i = end + 2
            continue
        if char == opening:
            depth += 1
        elif char == closing:
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise ContractParityError("unterminated source delimiter")


def signature(method, path):
    path = re.sub(r":([A-Za-z_][A-Za-z0-9_]*)", "{}", path)
    path = re.sub(r"\{[^{}]*\}", "{}", path)
    require(path.startswith("/") and "//" not in path, "invalid route: " + path)
    return method.upper() + " " + path


def mask_comments(text):
    """Keep offsets/lines and quoted literals; commented routes are not routes."""
    output, i = list(text), 0
    while i < len(text):
        if text[i] in ("'", '"', TICK):
            quote, i = text[i], i + 1
            while i < len(text):
                if text[i] == "\\" and quote != TICK:
                    i += 2
                elif text[i] == quote:
                    i += 1
                    break
                else:
                    i += 1
        elif text.startswith("//", i) or text.startswith("/*", i):
            line = text.startswith("//", i)
            end = text.find("\n" if line else "*/", i + 2)
            require(line or end >= 0, "unterminated source comment")
            end = len(text) if end < 0 else end + (0 if line else 2)
            for index in range(i, end):
                if text[index] != "\n":
                    output[index] = " "
            i = end
        else:
            i += 1
    return "".join(output)


def read_routes(sources):
    routes = {}
    for filename, text in sources.items():
        text = mask_comments(text)
        groups = {"group": ""}
        for match in re.finditer(r'(\w+)\s*:?=\s*(\w+)\.Group\("([^"]*)"\)', text):
            variable, parent, suffix = match.groups()
            require(parent in groups, filename + ": unresolved route group " + parent)
            require(variable not in groups, filename + ": reused route group variable " + variable)
            groups[variable] = groups[parent] + suffix
        registrations = list(re.finditer(r'\b(\w+)\.([A-Z]+)\(\s*"([^"]+)"', text))
        require(registrations, filename + ": no route registrations found")
        all_calls = re.findall(r'\b\w+\.(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE)\(', text)
        require(len(all_calls) == len(registrations), filename + ": HTTP registration has a nonliteral/unrecognized path")
        require(not re.search(r'\b\w+\.(?:Handle|Any)\(', text),
                filename + ": unsupported dynamic HTTP registration")
        for match in registrations:
            receiver, method, suffix = match.groups()
            require(method in METHODS, filename + ": unsupported HTTP method " + method)
            require(receiver in groups, filename + ": unresolved route receiver " + receiver)
            opening = text.index("(", match.start())
            block = text[opening + 1:close_at(text, opening)]
            request_type = None
            request_match = re.search(r'\b(?:input|in|i|_)\s+(core\.\w+|struct\s*\{|\w+)', block)
            if request_match:
                request_type = request_match.group(1)
                if request_type.startswith("struct"):
                    start = opening + 1 + request_match.end() - 1
                    request_type = text[start:close_at(text, start, "{", "}") + 1]
            require(method in {"GET", "HEAD", "OPTIONS"} or request_type is not None,
                    filename + ": unrecognized request decoder for " + method + " " + suffix)
            key = signature(method, groups[receiver] + suffix)
            require(key not in routes, "duplicate registered operation " + key)
            routes[key] = {"file": filename, "path": groups[receiver] + suffix,
                           "line": text.count("\n", 0, match.start()) + 1,
                           "request_type": request_type,
                           "fixed_success": "200" if re.search(r'\b(?:g2FullRead|g2FullPostRead|g3g6Read)\(', block) else
                                            (re.search(r'\bsendOK\(c,\s*(2\d\d),', block).group(1)
                                             if re.search(r'\bsendOK\(c,\s*(2\d\d),', block) else None),
                           "typed_response": (re.search(r'\)\s*\((\[\])?core\.(\w+),\s*error\)', block).group(0)
                                              if re.search(r'\)\s*\((\[\])?core\.(\w+),\s*error\)', block) else None),
                           "read_method": (re.search(r'\breturn\s+(?:service|s)\.(\w+)\(', block).group(1)
                                           if re.search(r'\breturn\s+(?:service|s)\.(\w+)\(', block) else None),
                           "idempotent": bool(re.search(r'\b(?:g2FullWrite|g3g6Write)\(', block))}
    return routes


def go_struct_fields(sources):
    definitions = {}
    for text in sources.values():
        text = mask_comments(text)
        for match in re.finditer(r'\btype\s+(\w+)\s+struct\s*\{', text):
            start = match.end() - 1
            definitions[match.group(1)] = text[start + 1:close_at(text, start, "{", "}")]

    def fields(name, seen=None):
        seen = set() if seen is None else seen
        name = name.removeprefix("core.")
        if name in seen:
            return {}
        seen = seen | {name}
        body = name[1:-1] if name.startswith("{") else definitions.get(name)
        require(body is not None, "cannot inspect Go request DTO " + name)
        result = {}
        for line in body.splitlines():
            tagged = re.search(r'^\s*\w+\s+(.+?)\s+' + TICK + r'.*?json:"([^",]+)', line)
            if tagged:
                go_type, json_name = tagged.groups()
                if json_name != "-":
                    result[json_name] = go_type.strip()
            else:
                embedded = re.fullmatch(r'\s*(?:core\.)?(\w+)\s*(?://.*)?', line)
                if embedded and embedded.group(1) in definitions:
                    result.update(fields(embedded.group(1), seen))
        return result
    return fields


def resolve(spec, schema, seen=None):
    require(isinstance(schema, dict), "schema must be an object")
    seen = set() if seen is None else seen
    if "$ref" in schema:
        ref = schema["$ref"]
        require(ref.startswith("#/"), "only local contract references are supported")
        require(ref not in seen, "cyclic root schema reference " + ref)
        target = spec
        try:
            for part in ref[2:].split("/"):
                target = target[part.replace("~1", "/").replace("~0", "~")]
        except KeyError as exc:
            raise ContractParityError("unresolved schema reference " + ref) from exc
        return resolve(spec, target, seen | {ref})
    if "allOf" in schema:
        merged = {"type": "object", "properties": {}, "required": []}
        for branch in schema["allOf"]:
            part = resolve(spec, branch, seen)
            merged["properties"].update(part.get("properties", {}))
            merged["required"] += part.get("required", [])
        return merged
    return schema


def normalize_openapi_schema(value):
    """OAS 3.0 nullable, including nullable references, as Draft 7 schemas."""
    if isinstance(value, list):
        return [normalize_openapi_schema(item) for item in value]
    if not isinstance(value, dict):
        return value
    result = {key: normalize_openapi_schema(item) for key, item in value.items() if key != "nullable"}
    if value.get("nullable"):
        if isinstance(result.get("type"), str):
            result["type"] = [result["type"], "null"]
        else:
            def shaped(schema):
                if not isinstance(schema, dict):
                    return False
                if isinstance(schema.get("type"), str):
                    return True
                if isinstance(schema.get("$ref"), str) and schema["$ref"].startswith("#/"):
                    return True
                return any(isinstance(schema.get(key), list) and schema[key] and
                           all(shaped(branch) for branch in schema[key]) for key in ("allOf", "anyOf", "oneOf"))
            require(shaped(result), "nullable schema needs a concrete type, local reference or nonempty composition")
            result = {"anyOf": [result, {"type": "null"}]}
    return result


def concrete(spec, schema, where):
    schema = resolve(spec, schema)
    if "oneOf" in schema or "anyOf" in schema:
        for branch in schema.get("oneOf", schema.get("anyOf", [])):
            concrete(spec, branch, where)
    elif schema.get("type") == "array":
        concrete(spec, schema.get("items", {}), where + " items")
    elif schema.get("type") == "object" or "properties" in schema:
        require(bool(schema.get("properties")) or
                (schema.get("additionalProperties") is False and schema.get("maxProperties") == 0),
                where + ": arbitrary/empty object schema")
    else:
        require(schema.get("type") in {"string", "integer", "number", "boolean", "null"},
                where + ": missing concrete schema")
    return schema


def split_expression(text, separator):
    parts, start, i = [], 0, 0
    while i < len(text):
        char = text[i]
        if char in ("'", '"', TICK):
            quote, i = char, i + 1
            while i < len(text):
                if text[i] == "\\":
                    i += 2
                elif text[i] == quote:
                    i += 1
                    break
                else:
                    i += 1
            continue
        if char in "([{":
            i = close_at(text, i, char, {"(": ")", "[": "]", "{": "}"}[char]) + 1
            continue
        if char == separator:
            parts.append(text[start:i].strip())
            start = i + 1
        i += 1
    return parts + [text[start:].strip()]


def client_url(expression, helpers, depth=0):
    require(depth < 12, "cyclic frontend URL helper")
    expression = expression.strip()
    parts = split_expression(expression, "+")
    if len(parts) > 1:
        return "".join(client_url(p, helpers, depth + 1) for p in parts)
    if expression[:1] in ("'", '"') and expression[-1:] == expression[:1]:
        return expression[1:-1]
    if expression.startswith(TICK) and expression.endswith(TICK):
        text, result, i = expression[1:-1], "", 0
        while i < len(text):
            start = text.find("$" + "{", i)
            if start < 0:
                return result + text[i:]
            result += text[i:start]
            end = close_at(text, start + 1, "{", "}")
            inner = text[start + 2:end]
            if "?" in inner:
                require(re.fullmatch(r'\w+\s*\?\s*' + TICK + r'\?[^' + TICK + r']*' + TICK + r"\s*:\s*''", inner),
                        "unsupported frontend conditional path " + inner)
                result += "?"
            else:
                result += client_url(inner, helpers, depth + 1)
            i = end + 1
        return result
    call = re.fullmatch(r'(\w+)\((.*)\)', expression, re.S)
    if call:
        name = call.group(1)
        if name in {"segment", "encodeURIComponent"}:
            return "{}"
        require(name in helpers, "unknown frontend URL helper " + name)
        return client_url(helpers[name], helpers, depth + 1)
    if re.fullmatch(r'[A-Za-z_]\w*(?:\.\w+)*', expression):
        return "{}"
    raise ContractParityError("unsupported frontend URL expression " + expression)


def read_clients(sources, api_prefix):
    calls = []
    for filename, text in sources.items():
        helpers = dict(re.findall(r'^const\s+(\w+)\s*=\s*\([^\n]*?\)\s*=>\s*([^\n]+)', text, re.M))
        matches = list(re.finditer(r'\b(get|postNoBody|post|put|patch|del)(?=<|\()', text))
        require(matches, filename + ": no frontend HTTP calls found")
        for match in matches:
            start = match.end()
            if text[start] == "<":
                start = close_at(text, start, "<", ">") + 1
            require(text[start] == "(", filename + ": unrecognized frontend HTTP call")
            end = close_at(text, start)
            arguments = split_expression(text[start + 1:end], ",")
            argument = arguments[0]
            path = client_url(argument, helpers).split("?", 1)[0]
            require(path.startswith(api_prefix + "/"), filename + ": unexpected API prefix " + path)
            method = {"postNoBody": "POST", "del": "DELETE"}.get(match.group(1), match.group(1).upper())
            function = list(re.finditer(r'export\s+const\s+(\w+)', text[:match.start()]))
            require(function, filename + ": HTTP call outside exported API consumer")
            calls.append({"file": filename, "function": function[-1].group(1),
                          "line": text.count("\n", 0, match.start()) + 1,
                          "body_mode": "none" if method in {"GET", "HEAD", "OPTIONS"} or match.group(1) == "postNoBody" else "json" if len(arguments) > 1 else "none",
                          "signature": signature(method, path[len(api_prefix):])})
    return calls


def check_document(spec, route_sources, client_sources, type_sources):
    routes = read_routes(route_sources)
    fields = go_struct_fields(type_sources)
    read_types = {}
    for text in type_sources.values():
        for match in re.finditer(r'^func \(s \*Service\) (\w+)\([^\n]*\) \((\[\])?(\w+), error\)', text, re.M):
            read_types[match.group(1)] = (bool(match.group(2)), match.group(3))
    operations = {}
    for path, item in spec.get("paths", {}).items():
        for method, operation in item.items():
            if method.upper() not in METHODS:
                continue
            key = signature(method, path)
            require(key not in operations, "ambiguous OpenAPI path " + key)
            operations[key] = operation
            source = operation.get("x-lingdoc-route-source")
            if source in {Path(p).name for p in ROUTE_FILES}:
                require(key in routes, "documented route is no longer registered: " + key)
                require(Path(routes[key]["file"]).name == source, "route source changed: " + key)
    for key, route in routes.items():
        require(key in operations, "registered operation absent from OpenAPI: " + key)
        operation = operations[key]
        parameters = operation.get("parameters", [])
        if route["idempotent"]:
            require(any(p.get("in") == "header" and p.get("name", "").lower() == "idempotency-key" and p.get("required")
                        for p in parameters), key + ": missing required Idempotency-Key")
        body = operation.get("requestBody")
        if route["request_type"] is not None:
            require(body and body.get("required"), key + ": missing required request body")
            request_schema = concrete(spec, body.get("content", {}).get("application/json", {}).get("schema", {}), key + " request")
            expected = fields(route["request_type"])
            require(set(expected) <= set(request_schema.get("properties", {})),
                    key + ": request schema omits Go JSON fields " + str(sorted(set(expected) - set(request_schema.get("properties", {})))))
            for name, go_type in expected.items():
                primitive = {"string": "string", "bool": "boolean", "int": "integer", "int64": "integer",
                             "uint64": "integer", "float64": "number"}.get(go_type.lstrip("*"))
                if primitive:
                    actual = resolve(spec, request_schema["properties"][name]).get("type")
                    require(actual == primitive, key + ": request field " + name + " disagrees with Go type " + go_type)
            if not expected:
                require(request_schema.get("additionalProperties") is False and request_schema.get("maxProperties") == 0,
                        key + ": struct{} must be an explicitly closed empty object")
        else:
            require(body is None, key + ": GET/read route unexpectedly declares a request body")
        successes = [value for status, value in operation.get("responses", {}).items() if re.fullmatch(r'2\d\d', str(status))]
        require(successes, key + ": missing success response")
        if route["fixed_success"]:
            require(route["fixed_success"] in operation["responses"], key + ": fixed handler success status is missing")
        for status, response in operation.get("responses", {}).items():
            if re.fullmatch(r'[45]\d\d', str(status)):
                error_schema = concrete(spec, response.get("content", {}).get("application/json", {}).get("schema", {}),
                                        key + " error " + str(status))
                error = resolve(spec, error_schema.get("properties", {}).get("error", {}))
                require({"code", "message"} <= set(error.get("properties", {})),
                        key + ": error response needs concrete code/message schema")
        for response in successes:
            envelope = concrete(spec, response.get("content", {}).get("application/json", {}).get("schema", {}), key + " response")
            data = envelope.get("properties", {}).get("data")
            require(data is not None, key + ": success response has no data schema")
            data_schema = concrete(spec, data, key + " response data")
            typed = route["typed_response"]
            expected_response = read_types.get(route["read_method"])
            if typed:
                match = re.search(r'(\[\])?core\.(\w+)', typed)
                expected_response = (bool(match.group(1)), match.group(2))
            if expected_response:
                array, name = expected_response
                require((data_schema.get("type") == "array") == array, key + ": response array/object shape disagrees with Go")
                item = resolve(spec, data_schema["items"]) if array else data_schema
                expected_fields = fields(name)
                require(set(expected_fields) <= set(item.get("properties", {})),
                        key + ": response schema omits Go JSON fields " + str(sorted(set(expected_fields) - set(item.get("properties", {})))))
    servers = spec.get("servers", [])
    require(servers, "contract needs the actual API server prefix")
    prefix = urlsplit(servers[0]["url"]).path.rstrip("/")
    require(prefix == "/api/v1/lingdoc", "unexpected LingDoc API prefix " + prefix)
    clients = read_clients(client_sources, prefix)
    for call in clients:
        require(call["signature"] in operations,
                call["file"] + ":" + str(call["line"]) + " " + call["function"] + " uses unpublished operation " + call["signature"])
        operation = operations[call["signature"]]
        require((operation.get("requestBody") is not None) == (call["body_mode"] == "json"),
                call["function"] + ": frontend JSON/bodyless mode disagrees with contract " + call["signature"])
    return {"result": "PASS", "scope": "three_full_workflow_registrars_and_four_api_consumers",
            "registered_operations": len(routes), "frontend_calls": len(clients),
            "frontend_calls_to_scoped_registrars": sum(c["signature"] in routes for c in clients),
            "routes": [{"signature": key, **value} for key, value in sorted(routes.items())],
            "consumers": clients,
            "limits": ["Static source parity, not Go compilation or live HTTP execution",
                       "Delegated G4 and other API modules are outside this bounded inventory",
                       "Concrete schema plus Go request fields and typed/read-service response root fields; opaque write-result DTO semantic equivalence remains review/runtime work"]}


def load_inputs(root):
    root = Path(root)
    read = lambda rel: (root / rel).read_text(encoding="utf-8-sig")
    routes = {rel: read(rel) for rel in ROUTE_FILES}
    clients = {rel: read(rel) for rel in CLIENT_FILES}
    types = dict(routes)
    types.update({str(p.relative_to(root)): p.read_text(encoding="utf-8-sig")
                  for p in sorted((root / "internal/lingdoc/workspacecore").glob("*.go"))
                  if not p.name.endswith("_test.go")})
    return routes, clients, types


def check(root, spec):
    return check_document(spec, *load_inputs(root))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--contract", type=Path)
    args = parser.parse_args()
    contract = args.contract or args.source_root / "docs/08-本轮实施方案/contracts/openapi.json"
    print(json.dumps(check(args.source_root, json.loads(contract.read_text(encoding="utf-8-sig"))), ensure_ascii=False))
