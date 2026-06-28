# hopscotch release

Új verzió kiadásának teljes folyamata. Minden lépést sorban végezz el, állj meg jóváhagyásra ahol jelezve van.

## 1. Változások összegyűjtése

Nézd át a commitokat az előző tag óta:
```
git log $(git describe --tags --abbrev=0)..HEAD --oneline
```

Csoportosítsd:
- **Új feature-ök** (feat:)
- **Javítások** (fix:)
- **Refaktorálás, belső változás** (refactor:, chore:)
- **Dokumentáció** (docs:)

## 2. Dokumentáció frissítése

A commitok alapján döntsd el mi igényel dokumentáció-frissítést:

- **README.md / docs/**: új feature-öknél add hozzá a leírást, használati példát. Ha vizuálisan érdemes bemutatni (pl. UI változás), ellenőrizd hogy a meglévő SVG mock-ok még helytállók-e, és ha nem, frissítsd őket kézzel (lásd: `docs/` könyvtár, SVG fájlok).
- **GitHub wiki**: ha van olyan viselkedésváltozás vagy konfiguráció amit a wiki leír, frissítsd. Navigálj a wiki repository-hoz (`../hopscotch.wiki/` ha létezik, egyébként kérdezd meg a felhasználót hol van).
- Ha apró változás és minden dokumentáció naprakész: jelezd hogy nem szükséges frissítés.

Ha dokumentáció-változás szükséges: csináld meg, majd commitold külön (`docs:` prefix).

## 3. Verziószám javaslat

A semantic versioning alapján (v MAJOR.MINOR.PATCH):
- **PATCH**: csak bugfix, belső refaktor, doc
- **MINOR**: új feature, backward-compatible változás
- **MAJOR**: breaking change (config formátum, API, viselkedés)

Javasolj verziószámot indoklással, és **várj jóváhagyásra**.

## 4. Tesztek átvizsgálása

Nézd át az új kódot és döntsd el:
- Van-e új logika amire nincs teszt, és érdemes lenne írni?
- Van-e meglévő teszt amit frissíteni kell az új viselkedés miatt?

Ha igen: javasold konkrétan (melyik csomag, mit tesztelne). **Várj jóváhagyásra** mielőtt tesztet írsz.

Ha nem szükséges: indokold röviden.

## 5. Tesztek futtatása lokálisan

```
go test ./...
```

Ha valamelyik elbukik: ne folytasd, elemezd és javítsd a hibát.

## 6. Összefoglaló — felhasználó jóváhagyása

Írd ki:

```
## Release összefoglaló: vX.Y.Z

### Új feature-ök
- ...

### Javítások
- ...

### Belső változások
- ...

### Dokumentáció
- ...
```

**Várj jóváhagyásra.** Ha a felhasználó javítást kér, végezd el, majd kezdd újra a 4. lépéstől.

## 7. Verzió kiadás

Ha a felhasználó jóváhagyta:

1. Verziószám beírása a `version.go`-ba vagy ahol tárolva van:
   ```
   grep -rn 'Version\s*=' internal/version/
   ```

2. Commit:
   ```
   git add internal/version/version.go
   git commit -m "chore: bump version to vX.Y.Z"
   ```

3. Tag:
   ```
   git tag vX.Y.Z
   ```

4. PR írás és branch merge — csak ha a felhasználó külön kéri (`gh pr create ...`).
   Egyébként csak a local commit + tag elegendő, push-ot a felhasználó végez.

## Amit NEM kell csinálni

- Ne push-olj, ne merge-elj PR-t automatikusan — mindig a felhasználó dönt
- Ne ugorj lépést, még ha triviálisnak tűnik is
- Ne commitolj dokumentáció-változást és kód-változást egy commitba
