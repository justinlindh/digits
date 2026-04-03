# Digits Cross-Household Phone Linking

## 1. Data Model

How households, phones, links, and contacts relate to each other.

```mermaid
erDiagram
    User ||--o{ HouseholdMember : "belongs to"
    Household ||--o{ HouseholdMember : "has"
    Household ||--o{ Phone : "owns"
    HouseholdLink }o--|| Household : "household_a"
    HouseholdLink }o--|| Household : "household_b"
    Phone ||--o{ Contact : "phone_id"
    Phone ||--o{ Contact : "contact_phone_id"
    Phone ||--o{ ContactInvite : "from_phone"
    Phone ||--o{ ContactInvite : "to_phone"

    User {
        uuid id PK
        string email
        string name
    }
    Household {
        uuid id PK
        string name
        bool call_history_enabled
    }
    HouseholdMember {
        uuid user_id FK
        uuid household_id FK
        string role "admin | member"
    }
    Phone {
        bigint id PK
        uuid household_id FK
        string number "e.g. 101"
        string name "Kitchen Phone"
        string hardware_id
        timestamp paired_at
    }
    HouseholdLink {
        uuid id PK
        uuid household_a_id FK
        uuid household_b_id FK "NULL while pending"
        string status "pending | active | revoked"
        string invite_code "8-char alphanumeric"
        uuid invited_by FK
        uuid accepted_by FK
    }
    Contact {
        uuid id PK
        bigint phone_id FK
        bigint contact_phone_id FK
        string name "display name"
    }
    ContactInvite {
        uuid id PK
        bigint from_phone_id FK
        bigint to_phone_id FK
        string from_name
        string to_name
        string status "pending | accepted | rejected"
    }
```

## 2. Household Linking Flow

The step-by-step process of connecting two households.

```mermaid
sequenceDiagram
    participant PA as Parent A<br/>(Smith household)
    participant Server as digits.family<br/>Server
    participant PB as Parent B<br/>(Jones household)

    Note over PA,PB: Phase 1: Household Link

    PA->>Server: Create invite link
    Server-->>PA: Invite code: "Xk9mQ2pL"
    PA->>PB: Share code (text, email, in person)
    PB->>Server: Accept invite "Xk9mQ2pL"
    Server->>Server: Verify no self-link<br/>Normalize IDs (a < b)<br/>Check number conflicts
    Server-->>PB: Link active ✓
    Server-->>PA: Email: "Jones family linked!"

    Note over PA,PB: Phase 2: Contact Invites (per phone pair)

    PA->>Server: Send contact invite<br/>Smith Kitchen → Jones Kitchen<br/>name: "Smith Kitchen"
    Server-->>PB: Pending invite notification
    PB->>Server: Accept invite<br/>name: "Jones Kitchen"
    Server->>Server: Create bidirectional contacts:<br/>Smith Kitchen → Jones Kitchen<br/>Jones Kitchen → Smith Kitchen
    Server-->>PA: contacts_updated nudge
    Server-->>PB: contacts_updated nudge

    Note over PA,PB: Phase 3: Phone Sync

    Note left of PA: Smith Kitchen Phone<br/>boots / reconnects
    PA->>Server: WebSocket: {"type":"sync"}
    Server-->>PA: {"type":"contacts",<br/>"contacts":[{"number":"102",<br/>"name":"Jones Kitchen"}]}
    Note left of PA: Cached to<br/>/data/contacts.json

    Note over PA,PB: Calling is now allowed between synced contacts
```

## 3. System Overview

High-level view of two linked households with phones and contacts.

```mermaid
graph TB
    subgraph smithHH["🏠 Smith Household"]
        direction TB
        smith_parent["👤 Parent (admin)"]
        smith_kitchen["📞 Kitchen Phone<br/>#101"]
        smith_bedroom["📞 Bedroom Phone<br/>#102"]
        smith_parent -.->|manages| smith_kitchen
        smith_parent -.->|manages| smith_bedroom
    end

    subgraph jonesHH["🏠 Jones Household"]
        direction TB
        jones_parent["👤 Parent (admin)"]
        jones_kitchen["📞 Kitchen Phone<br/>#201"]
        jones_kids["📞 Kids Room Phone<br/>#202"]
        jones_parent -.->|manages| jones_kitchen
        jones_parent -.->|manages| jones_kids
    end

    smithHH <===>|"🔗 Household Link<br/>(active, invite code accepted)"| jonesHH

    smith_kitchen <-->|"📇 Contact pair<br/>'Jones Kitchen' ↔ 'Smith Kitchen'"| jones_kitchen
    smith_bedroom <-->|"📇 Contact pair<br/>'Jones Kids' ↔ 'Smith Bedroom'"| jones_kids

    subgraph server["☁️ digits.family Server"]
        direction LR
        relay["Signaling Relay<br/>(WebSocket)"]
        db[("PostgreSQL<br/>households, phones,<br/>links, contacts")]
        sync["Contact Syncer"]
        relay --- db
        sync --- db
    end

    smith_kitchen -.-|WebSocket| relay
    smith_bedroom -.-|WebSocket| relay
    jones_kitchen -.-|WebSocket| relay
    jones_kids -.-|WebSocket| relay

    style smithHH fill:#1a365d,stroke:#63b3ed,color:#fff
    style jonesHH fill:#1a365d,stroke:#63b3ed,color:#fff
    style server fill:#2d3748,stroke:#a0aec0,color:#fff
```

## 4. Call Permission Flow

How the system decides whether two phones can call each other.

```mermaid
flowchart TD
    A["Phone A dials Phone B's number"] --> B{Same household?}
    B -->|Yes| C["✅ Call allowed<br/>(intra-household)"]
    B -->|No| D{Households linked?}
    D -->|No| E["❌ Call blocked<br/>'Not in contacts'"]
    D -->|Yes| F{A has B in contacts?}
    F -->|No| E
    F -->|Yes| G{B has A in contacts?}
    G -->|No| E
    G -->|Yes| H["✅ Call allowed<br/>(cross-household contact)"]
    H --> I["Server relays<br/>WebRTC signaling"]
    I --> J["Voice call established<br/>(Opus codec, peer-to-peer)"]

    style C fill:#276749,stroke:#68d391,color:#fff
    style H fill:#276749,stroke:#68d391,color:#fff
    style E fill:#9b2c2c,stroke:#fc8181,color:#fff
```

## 5. Revocation Flow

What happens when a household link is revoked.

```mermaid
sequenceDiagram
    participant PA as Parent A
    participant Server as Server
    participant PB as Parent B
    participant Phones as All Affected Phones

    PA->>Server: Revoke link with Jones household
    Server->>Server: UPDATE household_links<br/>SET status = 'revoked'
    Server->>Server: CASCADE DELETE all contacts<br/>between Smith & Jones phones
    Server-->>Phones: contacts_updated nudge<br/>(via WebSocket)
    Phones->>Server: {"type":"sync"}
    Server-->>Phones: Updated contact lists<br/>(Jones contacts removed)
    Note over Phones: Phones update local<br/>/data/contacts.json<br/>Jones numbers no longer callable
```
