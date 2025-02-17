package adx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/azure-kusto-go/kusto"
	"github.com/Azure/azure-kusto-go/kusto/data/table"
	"github.com/Azure/azure-kusto-go/kusto/data/value"
	"github.com/Azure/azure-kusto-go/kusto/unsafe"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type TableMapping struct {
	Name string
	Kind string
	Mapping string
	LastUpdatedOn value.DateTime
	Table string
	Database string
}

type Mapping struct {
	Column string `json:"column"`
	Path string `json:"path"`
	DataType string `json:"datatype"`
	Transform string `json:"transform"`
}

func resourceADXTableMapping() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceADXTableMappingCreateUpdate,
		UpdateContext: resourceADXTableMappingCreateUpdate,
		ReadContext:   resourceADXTableMappingRead,
		DeleteContext: resourceADXTableMappingDelete,

		Schema: map[string]*schema.Schema{
			"name": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         false,
				ValidateDiagFunc: stringIsNotEmpty,
			},
			"database_name": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         false,
				ValidateDiagFunc: stringIsNotEmpty,
			},

			"table_name": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         false,
				ValidateDiagFunc: stringIsNotEmpty,
			},

			"kind": {
				Type:             schema.TypeString,
				Required:         true,
				ForceNew:         false,
				ValidateDiagFunc: stringInSlice([]string{
					"Json",
				}),
			},
			"mapping" : {
				Type: schema.TypeList,
				Required: true,
				ForceNew: false,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"column": {
							Type:     schema.TypeString,
							Required: true,
						},
						"path": {
							Type:     schema.TypeString,
							Required: true,
						},
						"datatype": {
							Type: schema.TypeString,
							Required: true,
						},
						"transform": {
							Type: schema.TypeString,
							Optional: true,
						},
					},
				},
			},
			"last_updated_on": {
				Type: schema.TypeString,
				Optional: true,
				Computed: true,
			},
		},
	}
}

func resourceADXTableMappingCreateUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	client := meta.(*Meta).Kusto

	name := d.Get("name").(string)
	tableName := d.Get("table_name").(string)
	databaseName := d.Get("database_name").(string)
	kind := d.Get("kind").(string)
	mapping := expandTableMapping(d.Get("mapping").([]interface{}))

	kStmtOpts := kusto.UnsafeStmt(unsafe.Stmt{Add: true})
	createStatement := fmt.Sprintf(".create-or-alter table %s ingestion %s mapping '%s' '[%s]'", tableName, strings.ToLower(kind), name, mapping)

	_, err := client.Mgmt(ctx, databaseName, kusto.NewStmt("", kStmtOpts).UnsafeAdd(createStatement))
	if err != nil {
		return diag.Errorf("error creating Mapping %q (Table %q, Database %q): %+v", name, tableName, databaseName, err)
	}

	id := fmt.Sprintf("%s|%s|%s|%s|%s", client.Endpoint(), databaseName, tableName, strings.ToLower(kind), name)
	d.SetId(id)

	resourceADXTableMappingRead(ctx, d, meta)

	return diags
}

func resourceADXTableMappingRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics

	client := meta.(*Meta).Kusto

	id, err := parseADXTableMappingID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	kStmtOpts := kusto.UnsafeStmt(unsafe.Stmt{Add: true})
	showStatement := fmt.Sprintf(".show table %s ingestion %s mapping '%s'", id.TableName, strings.ToLower(id.Kind), id.Name)

	resp, err := client.Mgmt(ctx, id.DatabaseName, kusto.NewStmt("", kStmtOpts).UnsafeAdd(showStatement))
	if err != nil {
		return diag.Errorf("error reading Table %q (Database %q): %+v", id.Name, id.DatabaseName, err)
	}
	defer resp.Stop()

	var schemas []TableMapping
	err = resp.Do(
		func(row *table.Row) error {
			rec := TableMapping{}
			if err := row.ToStruct(&rec); err != nil {
				return fmt.Errorf("error parsing Table schema for Table %q (Database %q): %+v", id.Name, id.DatabaseName, err)
			}
			schemas = append(schemas, rec)
			return nil
		},
	)

	if err != nil {
		return diag.Errorf("%+v", err)
	}

	d.Set("table_name", schemas[0].Table)
	d.Set("database_name", schemas[0].Database)
	d.Set("kind", schemas[0].Kind)
	d.Set("mapping", flattenTableMapping(schemas[0].Mapping))
	d.Set("last_updated_on", schemas[0].LastUpdatedOn)


	return diags
}

func resourceADXTableMappingDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics

	client := meta.(*Meta).Kusto

	id, err := parseADXTableMappingID(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	kStmtOpts := kusto.UnsafeStmt(unsafe.Stmt{Add: true})
	deleteStatement := fmt.Sprintf(".drop table %s ingestion %s mapping '%s'", id.TableName, strings.ToLower(id.Kind), id.Name)

	_, err = client.Mgmt(ctx, id.DatabaseName, kusto.NewStmt("", kStmtOpts).UnsafeAdd(deleteStatement))
	if err != nil {
		return diag.Errorf("error deleting Table Mapping %q (Table %q, Database %q): %+v", id.Name, id.TableName, id.DatabaseName, err)
	}

	d.SetId("")

	return diags
}

func expandTableMapping(input []interface{}) string {
	if len(input) == 0 {
		return ""
	}

	mappings := make([]string, 0)
	for _, v := range input {
		block := v.(map[string]interface{})
		mapping := fmt.Sprintf(`"column":"%s","path":"%s","datatype":"%s"`, block["column"].(string), block["path"].(string), block["datatype"].(string))
		if t, ok := block["transform"].(string); ok {
			if len(t) != 0 {
				mapping = fmt.Sprintf(`%s,"transform":"%s"`, mapping, t)
			}
		}
		mapping = fmt.Sprintf("{%s}", mapping)
		mappings = append(mappings, mapping)
	}
	return strings.Join(mappings, ",")
}

func flattenTableMapping(input string) []interface{} {
	if len(input) == 0 {
		return []interface{}{}
	}

	var oMappings []Mapping
	json.Unmarshal([]byte(input), &oMappings)

	mappings := make([]interface{}, 0)
	for _, v := range oMappings {
		block := make(map[string]interface{})
		block["column"] = v.Column
		block["path"] = v.Path
		block["datatype"] = v.DataType
		block["transform"] = v.Transform
		mappings = append(mappings, block)
	}
	return mappings
}
