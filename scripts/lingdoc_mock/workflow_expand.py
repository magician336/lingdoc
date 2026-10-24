"""The one implementation of the contract's value expansion rules.

Why this module exists
----------------------
A contract says two things about a value it sends:

* it may name a value it captured earlier -- ``{{name}}``.  A placeholder that
  occupies a whole value keeps the captured value's type (an int stays an int,
  a list stays a list); a placeholder inside a text is replaced by the
  captured value's text.
* it may have to say something about a collection whose *size is decided at run
  time*.  The F01 contract needs exactly that: every review item of a chapter
  needs one decision, and how many review items there are is not known until
  the model has answered.  A literal list cannot say that, so the contract
  writes a projection instead::

      {"$expand": {"collection": "question_review_items",
                   "item": {"review_item_id": {"$item": "id"},
                            "disposition": "retained_warning",
                            "reason": "..."}}}

  ``collection`` names a captured variable whose value is an array, ``item`` is
  the template one element is rendered from, and ``{"$item": "<field>"}``
  reads one field of the current element.  The expansion replaces the whole
  node with an array, so the wire format is unchanged: what reaches the server
  is the array the schema already describes.

Two callers use these rules: the F01 runner (``scripts/lingdoc_mock/run_f01.py``)
and the contract's static validator
(``docs/08-本轮实施方案/contracts/validate_artifacts.py``).  They used to carry
their own copies of the ``{{name}}`` rules, and two copies drift.  This module
is the one copy, and both callers delegate here.

How a failure is reported
--------------------------
Every failure raises :class:`ExpansionError`.  A caller that has its own error
type catches it and raises its own (``WorkflowError`` for the runner,
``AssertionError`` for the validator), so a caller can always tell "this value
could not be expanded" from any other error.  Nothing here ever guesses: a
collection that was never captured, a collection that is not an array, an
element that does not carry the field and a malformed ``$expand`` node are all
refusals -- never a silently empty array, never a silently dropped item.
"""
from __future__ import annotations

import re
from typing import Any

# A variable name may be namespaced the way the contract spells its own facts
# ("{{asset.k-notready}}"), so dots and dashes are part of a name, not separators.
VARIABLE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_.-]*)\}\}")
NAME = re.compile(r"[A-Za-z_][A-Za-z0-9_.-]*")

EXPAND_KEY = "$expand"
ITEM_KEY = "$item"
COLLECTION_KEY = "collection"
ITEM_TEMPLATE_KEY = "item"


class ExpansionError(Exception):
    """A contract value could not be expanded against the captured variables."""


def expand_value(value: Any, variables: dict[str, Any]) -> Any:
    """Expand one contract value against the variables captured so far.

    `variables` maps a captured name to the value the provider answered with.
    The result is the value to send: a dict, a list or a scalar, with every
    placeholder resolved and every ``$expand`` node projected.
    """
    return _expand(value, variables, None)


def referenced_collections(value: Any) -> set[str]:
    """The names of the collections the contract projects over.

    A collection is named by a captured variable, not by a ``{{name}}``
    placeholder, so it does not show up in a scan for placeholders.  A caller
    that wants to know which variables a contract needs asks here.
    """
    found: set[str] = set()
    _collect_collections(value, found)
    return found


def _expand(value: Any, variables: dict[str, Any], element: Any) -> Any:
    if isinstance(value, dict):
        if EXPAND_KEY in value:
            return _expand_collection(value, variables)
        return {key: _expand(child, variables, element) for key, child in value.items()}
    if isinstance(value, list):
        return [_expand(child, variables, element) for child in value]
    if not isinstance(value, str):
        return value
    exact = VARIABLE.fullmatch(value)
    if exact:
        name = exact.group(1)
        if name not in variables:
            raise ExpansionError(f"workflow variable is not captured yet: {name}")
        return variables[name]

    def replace(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in variables:
            raise ExpansionError(f"workflow variable is not captured yet: {name}")
        return str(variables[name])

    return VARIABLE.sub(replace, value)


def _expand_collection(node: dict[str, Any], variables: dict[str, Any]) -> list[Any]:
    """Project one collection into the array the request body carries."""
    if set(node) != {EXPAND_KEY}:
        raise ExpansionError(
            f"an $expand node holds $expand and nothing else: {sorted(node)}")
    directive = node[EXPAND_KEY]
    if not isinstance(directive, dict):
        raise ExpansionError("$expand must be an object")
    unknown = set(directive) - {COLLECTION_KEY, ITEM_TEMPLATE_KEY}
    if unknown:
        raise ExpansionError(f"$expand does not accept these keys: {sorted(unknown)}")
    if COLLECTION_KEY not in directive:
        raise ExpansionError("$expand must name the collection it projects over")
    if ITEM_TEMPLATE_KEY not in directive:
        raise ExpansionError("$expand must declare the item template")
    collection = directive[COLLECTION_KEY]
    if not isinstance(collection, str) or not collection.strip():
        raise ExpansionError("$expand collection must be the name of a captured variable")
    if "{{" in collection:
        raise ExpansionError(
            f"$expand collection must name a captured variable, not a placeholder: {collection}")
    if not NAME.fullmatch(collection):
        raise ExpansionError(f"$expand collection is not a variable name: {collection}")
    if collection not in variables:
        raise ExpansionError(f"workflow variable is not captured yet: {collection}")
    values = variables[collection]
    if not isinstance(values, list):
        raise ExpansionError(
            f"workflow variable {collection} must hold an array, not {type(values).__name__}")
    template = directive[ITEM_TEMPLATE_KEY]
    return [_expand_element(template, variables, element, collection) for element in values]


def _expand_element(template: Any, variables: dict[str, Any], element: Any,
                    collection: str) -> Any:
    """Render the item template for one element of the collection."""
    if isinstance(template, dict):
        if ITEM_KEY in template:
            if set(template) != {ITEM_KEY}:
                raise ExpansionError(
                    f"a $item node holds $item and nothing else: {sorted(template)}")
            field = template[ITEM_KEY]
            if not isinstance(field, str) or not field.strip():
                raise ExpansionError(f"$item must name a field of the element: {collection}")
            if not isinstance(element, dict):
                raise ExpansionError(
                    f"$item reads the field {field} of an element that is not an object: "
                    f"{collection}")
            if field not in element:
                raise ExpansionError(
                    f"$item field is missing on the element: {field} (collection {collection})")
            return element[field]
        return {key: _expand_element(child, variables, element, collection)
                for key, child in template.items()}
    if isinstance(template, list):
        return [_expand_element(child, variables, element, collection) for child in template]
    return _expand(template, variables, element)


def _collect_collections(value: Any, found: set[str]) -> None:
    if isinstance(value, dict):
        directive = value.get(EXPAND_KEY)
        if isinstance(directive, dict):
            collection = directive.get(COLLECTION_KEY)
            if isinstance(collection, str) and NAME.fullmatch(collection):
                found.add(collection)
        for child in value.values():
            _collect_collections(child, found)
    elif isinstance(value, list):
        for child in value:
            _collect_collections(child, found)
