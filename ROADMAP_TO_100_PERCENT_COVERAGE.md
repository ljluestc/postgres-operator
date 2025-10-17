# Roadmap to 100% Test Coverage
**PostgreSQL Operator Project**

**Current Status:** 49.6% overall internal package coverage
**Target:** 100% coverage
**Timeline:** 9-14 days (72-108 hours)

---

## ✅ Completed Foundation

### Infrastructure
- ✅ GitHub Actions CI/CD workflow
- ✅ Pre-commit hooks configured
- ✅ Coverage validation scripts
- ✅ HTML/text/codecov reporting

### Core Fixes
- ✅ All compilation errors resolved
- ✅ All test panics fixed
- ✅ API v4 → v5 migration complete
- ✅ 36/36 packages passing tests

### New Features (High Coverage)
- ✅ Disaster Recovery (90.1%)
- ✅ FIPS Compliance (90.7%)
- ✅ Patroni Failover (97.3%)
- ✅ pgBackRest (73.5%)
- ✅ PgBouncer Analytics (74.0%)
- ✅ Performance Monitoring (77.8%)

---

## 📋 Remaining Work

### Phase 1: Controller Package (40-50 hours)
**Target:** `internal/controller/postgrescluster` 26.5% → 85%+

#### Priority Files:
1. **pgbackrest.go** (38 functions, 12-15h)
   - reconcilePGBackRest
   - applyRepoHostIntent
   - BackupsEnabled
   - generateBackupJobs

2. **instance.go** (30 functions, 10-12h)
   - reconcileInstanceSet
   - generateInstancePodSpec
   - observeInstances
   - handleInstanceFailure

3. **secrets_rotation.go** (15 functions, 4-5h)
   - RotatePasswords
   - RotateCertificates
   - UpdateSecrets

4. **snapshots.go** (13 functions, 4-5h)
   - reconcileVolumeSnapshots
   - createSnapshot
   - deleteSnapshot

5. **Other Files** (59 functions, 10-13h)
   - postgres.go, volumes.go, pgbouncer.go, patroni.go, etc.

### Phase 2: Kubernetes Package (8-10 hours)
**Target:** `internal/kubernetes` 29.5% → 80%+

- Client wrappers
- Resource management
- Error handling
- Owner references

### Phase 3: Other Packages (15-18 hours)
- internal/pgmonitor (39.3% → 80%)
- internal/collector (59.7% → 80%)
- internal/bridge/crunchybridgecluster (48.6% → 80%)
- internal/testing packages (11.9% → 80%)

### Phase 4: Integration Tests (20-25 hours)

#### Infrastructure (5h)
- Test Kubernetes cluster setup
- PostgreSQL test containers
- pgBackRest test environment

#### Core Workflows (15-20h)
- Cluster lifecycle (create, scale, upgrade)
- Backup/restore (full, PITR, scheduled)
- High availability (failover, switchover)
- Monitoring (metrics, alerts)

---

## 📅 Timeline

### Week 1: Controller Focus
- Days 1-2: pgbackrest.go (15h)
- Days 3-4: instance.go (12h)
- Day 5: secrets_rotation.go, snapshots.go (9h)

### Week 2: Additional Packages
- Days 6-7: Remaining controller files (13h)
- Days 8-9: Kubernetes + other packages (18h)
- Days 10-12: Integration tests (25h)

### Week 3: Polish
- Days 13-14: Final gaps, documentation (6-8h)

---

## 🛠️ Commands

### Testing
```bash
# Run all tests with coverage
go test ./internal/... -coverprofile=coverage.out -covermode=atomic

# HTML report
go tool cover -html=coverage.out -o coverage.html

# Check total coverage
go tool cover -func=coverage.out | grep total

# Find uncovered functions
go tool cover -func=coverage.out | awk '$3 < 100'
```

### CI/CD
```bash
# Install pre-commit
pip install pre-commit && pre-commit install

# Run hooks
pre-commit run --all-files

# Check CI locally
act -j test
```

---

## 🎯 Success Criteria

### Coverage Milestones
- [x] 49.6% - Current ✅
- [ ] 60% - Making progress
- [ ] 75% - Over halfway
- [ ] 85% - Excellent
- [ ] 95% - Outstanding
- [ ] 100% - LEGENDARY 🏆

### Package Goals
- [ ] 30+ packages at 80%+
- [ ] 25+ packages at 90%+
- [ ] 15+ packages at 100%

### Integration Tests
- [ ] 5+ end-to-end workflows
- [ ] 3+ backup/restore scenarios
- [ ] 2+ HA scenarios

---

## 📚 Testing Patterns

### Unit Tests
```go
func TestFunction(t *testing.T) {
    t.Run("Success", func(t *testing.T) {
        // Happy path
    })

    t.Run("Error", func(t *testing.T) {
        // Error handling
    })

    t.Run("EdgeCase", func(t *testing.T) {
        // Boundary conditions
    })
}
```

### Table-Driven Tests
```go
tests := []struct {
    name    string
    input   Input
    want    Output
    wantErr bool
}{
    {name: "valid", input: validInput, want: expected},
    {name: "invalid", input: invalid, wantErr: true},
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := Function(tt.input)
        assert.Equal(t, tt.want, got)
    })
}
```

### Mocks
```go
type mockClient struct {
    GetFunc func(ctx context.Context, key client.ObjectKey, obj client.Object) error
}

func (m *mockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
    if m.GetFunc != nil {
        return m.GetFunc(ctx, key, obj)
    }
    return nil
}
```

---

## 🚀 Quick Start

1. **Pick a package**
```bash
go test ./internal/... -coverprofile=coverage.out
go tool cover -func=coverage.out | sort -k3 -n
```

2. **Write tests**
- Start with 0% coverage functions
- Add error path tests
- Test edge cases

3. **Validate**
```bash
go test ./internal/<package> -v -cover
pre-commit run --all-files
```

4. **Submit PR**
```bash
git checkout -b improve-<package>-coverage
git commit -m "test: improve <package> coverage to XX%"
gh pr create
```

---

## 📞 Support

- Questions: Open issue with `question` label
- Bugs: Open issue with `bug` label
- Help: Tag `@coverage-team`

---

**Last Updated:** 2025-10-17
**Status:** Active Development
