<!--
# Copyright 2025 Crunchy Data Solutions, Inc.
#
# SPDX-License-Identifier: Apache-2.0
-->

# Custom Resource Definition Schemas

These directories contain Go types that [controller-gen] uses to generate matching [CRD] schemas.
The CRD schema tells Kubernetes what fields and values are allowed in our API objects and how to handle changes to values.

> [!TIP]
> The CRD schema is *most* of the UX of our API

CRD schemas are modified OpenAPI 3.0 [validation] schemas.
Much of the schema defines what fields, types, and values are *allowed*.
`controller-gen` considers the field's [Go type] and [validation markers] for this.

Kubernetes uses its own algorithm to consider and accept changes to API objects: [Server-Side Apply], SSA.
CRD schemas contain non-standard attributes that affect SSA.
`controller-gen` considers the [processing markers] of a field for this.

🤔 Validation is *what* and SSA/processing is *how* 🤔

[controller-gen]: https://book.kubebuilder.io/reference/controller-gen
[CRD]: https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions
[processing markers]: https://book.kubebuilder.io/reference/markers/crd-processing
[Server-Side Apply]: https://docs.k8s.io/reference/using-api/server-side-apply
[validation]: https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#validation
[validation markers]: https://book.kubebuilder.io/reference/markers/crd-validation


# OpenAPI Properties

Kubernetes supports a subset of the OpenAPI schema definition, which is a subset of an old draft of [JSON schema].

Fields can have these properties:

- `enum` is a list of values allowed in the field.
- `required` is a boolean indicating the field must be present in whatever payload.
  This does *NOT* indicate anything about the value being `null`, "zero," or "empty."
- `nullable` is a boolean indicating if the value can be `null`.
  When this is omitted or `false`, a `null` value is removed from whatever payload or replaced with the field's [default value].
- `type` is one of `integer`, `number`, `string`, `boolean`, `array`, or `object`.
- `format` constrains the `type` a bit further.
  It affects how `kubectl` presents the field and how the value turns up in CEL validation.

Numeric fields can have these properties:

- `minimum` and `maximum` are the smallest and largest numbers allowed in the field.
- `exclusiveMinimum` and `exclusiveMaximum` are booleans that indicate if the exact values above are allowed.
  That is, `true` here changes min and max from ≥ and ≤ to > and <.
- `multipleOf` is a number. Values in the field must be nicely divisible by this number.

String fields can have these properties:

- `minLength` and `maxLength` are the smallest and largest number of characters allowed in the field.
- `pattern` is a regular expression describing values allowed in the field.
  In Kubernetes, this is an [RE2] expression, *NOT* an ECMA expression.

Array fields can have these properties:

- `items` is a schema that describes what values are allowed inside the field.
- `minItems` and `maxItems` are the smallest and largest number of items allowed in the field.

The value of an object field is an unordered set of key/value pairs; a JSON object, a YAML mapping.
Kubernetes supports only two ways to treat the keys of those values: known or unknown.

The `properties` property indicates that the keys are known; these fields can have these properties:

- `properties` is an unordered set of key/value pairs (JSON object, YAML mapping) in which:
  - the key is the name of a field allowed in the object
  - the value is a schema that describes what values are allowed in that field.

The `additionalProperties` property indicates that the keys are unknown; these fields can have these properties:

- `additionalProperties` is a schema that describes what values are allowed in every... key-value value of the field.
- `minProperties` and `maxProperties` are the smallest and largest number of key-value keys allowed in the field.

> [!TIP]
> `properties` is like a Go struct // `additionalProperties` is like a Go map

[default value]: https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#defaulting-and-nullable
[JSON schema]: https://json-schema.org/draft-06
[RE2]: https://github.com/google/re2#syntax


# CEL Rules

> [!IMPORTANT]
> When possible, use [OpenAPI properties](#openapi-properties) rather than CEL rules.
> The former do not affect the CRD [validation budget](#cel-validation-budget).

## Optional field syntax

CEL offers a safe traversal/retrieval through the use of `?` as an [optional field marker].

As an example, when attempting to retrieve `self.config.global.logfile`, in older (but still supported)
versions of k8s, we may need to incrementally check the path: `has(self.config) && has(self.config.global)...`

With the optional field marker, we can use that field safely without checking the entire path:
`self.?config.global.logfile` will return a value or no value; and anything using that value will
likewise be considered optional.

The optional field syntax is only available in K8s 1.29+.

[optional field marker]: https://pkg.go.dev/github.com/google/cel-go/cel#hdr-Syntax_Changes-OptionalTypes.

## CEL Availability

Kubernetes' capabilities with CEL are continuously expanding.
Different versions of Kubernetes have different CEL functions, syntax, and features.

```asciidoc
:controller-tools: https://github.com/kubernetes-sigs/controller-tools/releases

[cols=",,", options="header"]
|===
| Kubernetes | OpenShift | `controller-gen`

| 1.25 Beta, `CustomResourceValidationExpressions` gate
| OCP 4.12
| link:{controller-tools}/v0.9.0[v0.9.0] has `rule` and `message` fields on the `XValidation` marker

| 1.27 adds `messageExpression`
| OCP 4.14
| link:{controller-tools}/v0.15.0[v0.15.0] adds `messageExpression` field to the `XValidation` marker

| 1.28 adds `reason` and `fieldPath`
| OCP 4.15
| link:{controller-tools}/v0.16.0[v0.16.0] adds `reason` and `fieldPath` to the `XValidation` marker

| 1.29 GA | OCP 4.16 |

| 1.30 enables link:#validation-ratcheting[validation ratcheting]; link:https://pr.k8s.io/123475[fixes fieldPath]…
| OCP 4.17
| link:{controller-tools}/v0.17.3[v0.17.3] adds `optionalOldSelf` to the `XValidation` marker

| 1.34 link:https://pr.k8s.io/132837[fixes IntOrString cost]
| ?
| link:{controller-tools}/v0.18.0[v0.18.0] allows validation on IntOrString

| 1.35 link:https://pr.k8s.io/132798[shows values when validation fails]
| ?
| n/a

|===
```

<!-- TODO: long-form; describe each library -->

Some details are missing from the Go package documentation: https://pr.k8s.io/130660

| CEL [libraries](https://code.k8s.io/staging/src/k8s.io/apiserver/pkg/cel/library), extensions, etc. | Kubernetes | OpenShift |
| --- | --- | --- |
| kubernetes.authz | 1.28 |
| kubernetes.authzSelectors | 1.32 |
| kubernetes.format | 1.32 | [4.18](https://github.com/openshift/kubernetes/pull/2140) |
| kubernetes.lists | 1.24 | 4.12 |
| kubernetes.net.cidr | 1.31 | [4.16](https://github.com/openshift/kubernetes/pull/1828) |
| kubernetes.net.ip | 1.31 | [4.16](https://github.com/openshift/kubernetes/pull/1828) |
| kubernetes.quantity | 1.29 | 4.16 |
| kubernetes.regex | 1.24 | 4.12 |
| kubernetes.urls | 1.24 | 4.12 |
| [cross-type numeric comparison](https://pkg.go.dev/github.com/google/cel-go/cel#CrossTypeNumericComparisons) | 1.29 | 4.16 |
| [optional types](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes) | 1.29 | 4.16 |
| [strings](https://pkg.go.dev/github.com/google/cel-go/ext#Strings) v0 | 1.24 | 4.12 |
| [strings](https://pkg.go.dev/github.com/google/cel-go/ext#Strings) v2 | 1.30 | 4.17 |
| [sets](https://pkg.go.dev/github.com/google/cel-go/ext#Sets) | 1.30 | 4.17 |
| [two-variable comprehension](https://pkg.go.dev/github.com/google/cel-go/ext#TwoVarComprehensions) | 1.33 |


# Validation Ratcheting

> **Feature Gate:** `CRDValidationRatcheting`
>
> Enabled in Kubernetes 1.30 and GA in 1.33 (OpenShift 4.17 and ~4.20)

[Validation ratcheting] allows update operations to succeed when unchanged fields are invalid.
This allows CRDs to add or "tighten" validation without breaking existing CR objects.

Some schema changes are not ratcheted:

- OpenAPI `allOf`, `oneOf`, `anyOf`, `not`; values in fields with these must be valid
- OpenAPI `required`; required fields are always required
- Removing `additionalProperties`; undefined fields are always dropped
- Adding or removing fields (names) in `properties`; undefined fields are dropped, and values in new fields must be valid
- Changes to `x-kubernetes-list-type` or `x-kubernetes-list-map-keys`; values in these fields must be valid
- Rules containing `oldSelf`; these are [transition rules] and should do their own ratcheting

[transition rules]: https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#transition-rules
[Validation ratcheting]: https://docs.k8s.io/tasks/extend-kubernetes/custom-resources/custom-resource-definitions#validation-ratcheting


# CEL Validation Budget

Kubernetes enforces computational limits on CEL validation rules to prevent excessive API server resource consumption. These limits are measured in platform-independent "cost units" that estimate execution complexity.

## Why OpenAPI Properties Are Preferred

**OpenAPI schema validation** (like `pattern`, `minimum`, `maximum`, `minLength`, etc.) **does not consume validation budget**. These validations are performed by the Kubernetes API server's schema validator without CEL evaluation overhead.

**CEL rules** consume validation budget based on their complexity and the size of data they process. This creates two important limitations:

### Two Types of Cost Limits

1. **Estimated Cost Limit** (Compile-time)
   - CEL statically computes the worst-case runtime cost of expressions
   - Rules with poor estimated cost are rejected when the CRD is created
   - Typically hit by O(n²) or worse operations over unbounded collections
   - Example: nested loops over arrays without `maxItems` constraints

2. **Runtime Cost Limit** (Evaluation-time)
   - Tracks actual cost during CEL expression evaluation
   - Halts execution if limit exceeded during API request validation
   - Rarely hit if estimated cost limit is already satisfied
   - Protects against pathological input data

### Common Budget Errors

When validation rules exceed budget, you'll see errors like:

```
CEL rule exceeded budget by more than 100x (try simplifying the rule,
or adding maxItems, maxProperties, and maxLength where arrays, maps,
and strings are used)
```

### Best Practices

1. **Use OpenAPI validation when possible**
   ```yaml
   # Good: No CEL cost
   pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
   minimum: 1
   maximum: 100
   ```

2. **Add size constraints to collections processed by CEL**
   ```yaml
   # Before: Unbounded cost
   x-kubernetes-validations:
     - rule: "self.items.all(x, x.value > 0)"

   # After: Bounded cost
   maxItems: 100
   x-kubernetes-validations:
     - rule: "self.items.all(x, x.value > 0)"
   ```

3. **Simplify complex CEL expressions**
   - Break complex rules into multiple simpler rules
   - Avoid nested comprehensions when possible
   - Use early termination with `exists()` instead of `all()` where applicable

4. **Add constraints to strings, arrays, and maps**
   ```yaml
   properties:
     config:
       type: object
       maxProperties: 50  # Bounds iteration cost
       additionalProperties:
         type: string
         maxLength: 1024  # Bounds string operation cost
   ```

### Cost Estimation Guidelines

Operations have different costs:
- Simple comparisons (`==`, `!=`, `<`, `>`): Low cost
- String operations (`contains`, `matches`, `startsWith`): Proportional to string length
- Array comprehensions (`all`, `exists`, `filter`, `map`): Proportional to array size
- Nested operations: Multiplicative cost (O(n²) or worse)

### When CEL Is Necessary

Use CEL rules when validation requires:
- Cross-field validation (`spec.replicas` must be less than `spec.maxReplicas`)
- Conditional requirements (field X required if field Y is set)
- Complex business logic that can't be expressed in OpenAPI
- Transition rules using `oldSelf`

In these cases, always add appropriate `maxItems`, `maxProperties`, and `maxLength` constraints to fields processed by the CEL rule.

### References

- [Common Expression Language in Kubernetes](https://kubernetes.io/docs/reference/using-api/cel/)
- [CRD Validation Rules (Beta)](https://kubernetes.io/blog/2022/09/23/crd-validation-rules-beta/)
- [CEL Cost Budget Issue](https://github.com/kubernetes/kubernetes/issues/121162)
