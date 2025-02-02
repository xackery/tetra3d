package tetra3d

import (
	"bytes"
	"encoding/json"
	"image"
	"log"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/kvartborg/vector"
	"github.com/qmuntal/gltf"
	"github.com/qmuntal/gltf/ext/lightspuntual"
	"github.com/qmuntal/gltf/modeler"

	_ "image/png"
)

type GLTFLoadOptions struct {
	CameraWidth, CameraHeight int  // Width and height of loaded Cameras. Defaults to 1920x1080.
	CameraDepth               bool // If cameras should render depth or not
	DefaultToAutoTransparency bool // If DefaultToAutoTransparency is true, then opaque materials become Auto transparent materials in Tetra3D.
	// DependentLibraryResolver is a function that takes a relative path (string) to the blend file representing the dependent Library that the loading
	// Library requires. This function should return a reference to the dependent Library; if it returns nil, the linked objects from the dependent Library
	// will not be instantiated in the loading Library.
	// An example would be loading a level (level.gltf) composed of assets from another file (a GLTF file exported from assets.blend, which is a directory up).
	// In this example, loading level.gltf would require the dependent library, found in assets.gltf. Loading level.gltf will refer to objects linked from the assets
	// blend file, known as "../assets.blend".
	// You could then simply load the assets library first and then code the DependentLibraryResolver function to take the assets library, or code the
	// function to use the path to load the library on demand. You could then store the loaded result as necessary if multiple levels use this assets Library.
	DependentLibraryResolver func(blendPath string) *Library
}

// DefaultGLTFLoadOptions creates an instance of GLTFLoadOptions with some sensible defaults.
func DefaultGLTFLoadOptions() *GLTFLoadOptions {
	return &GLTFLoadOptions{
		CameraWidth:               1920,
		CameraHeight:              1080,
		CameraDepth:               true,
		DefaultToAutoTransparency: true,
	}
}

// LoadGLTFFile loads a .gltf or .glb file from the filepath given, using a provided GLTFLoadOptions struct to alter how the file is loaded.
// Passing nil for loadOptions will load the file using default load options. Unlike with DAE files, Animations (including armature-based
// animations) and Cameras (assuming they are exported in the GLTF file) will be parsed properly.
// LoadGLTFFile will return a Library, and an error if the process fails.
func LoadGLTFFile(path string, loadOptions *GLTFLoadOptions) (*Library, error) {

	fileData, err := os.ReadFile(path)

	if err != nil {
		return nil, err
	}

	return LoadGLTFData(fileData, loadOptions)

}

// LoadGLTFData loads a .gltf or .glb file from the byte data given, using a provided GLTFLoadOptions struct to alter how the file is loaded.
// Passing nil for loadOptions will load the file using default load options. Unlike with DAE files, Animations (including armature-based
// animations) and Cameras (assuming they are exported in the GLTF file) will be parsed properly.
// LoadGLTFFile will return a Library, and an error if the process fails.
func LoadGLTFData(data []byte, gltfLoadOptions *GLTFLoadOptions) (*Library, error) {

	decoder := gltf.NewDecoder(bytes.NewReader(data))

	doc := gltf.NewDocument()

	err := decoder.Decode(doc)

	if err != nil {
		return nil, err
	}

	if gltfLoadOptions == nil {
		gltfLoadOptions = DefaultGLTFLoadOptions()
	}

	library := NewLibrary()

	var images []*ebiten.Image

	exportedTextures := true

	type Collection struct {
		Objects []string
		Offset  []float64
		Path    string
	}

	collections := map[string]Collection{}

	t3dExport := false

	if len(doc.Scenes) > 0 {

		if doc.Scenes[0].Extras != nil {

			globalExporterSettings := doc.Scenes[0].Extras.(map[string]interface{})

			if et, exists := globalExporterSettings["t3dPackTextures__"]; exists {
				t3dExport = true
				exportedTextures = et.(float64) > 0
			}

			if col, exists := globalExporterSettings["t3dCollections__"]; exists {
				t3dExport = true
				data := col.(map[string]interface{})

				jsonData, err := json.Marshal(data)
				if err != nil {
					panic(err)
				}

				err = json.Unmarshal(jsonData, &collections)

				if err != nil {
					panic(err)
				}

			}

		}

	}

	if exportedTextures {
		images = make([]*ebiten.Image, len(doc.Images))
		for i, gltfImage := range doc.Images {

			imageData, err := modeler.ReadBufferView(doc, doc.BufferViews[*gltfImage.BufferView])
			if err != nil {
				return nil, err
			}

			byteReader := bytes.NewReader(imageData)

			img, _, err := image.Decode(byteReader)
			if err != nil {
				return nil, err
			}

			images[i] = ebiten.NewImageFromImage(img)

		}

	}

	for _, gltfMat := range doc.Materials {

		newMat := NewMaterial(gltfMat.Name)
		newMat.library = library

		newMat.BackfaceCulling = !gltfMat.DoubleSided

		if texture := gltfMat.PBRMetallicRoughness.BaseColorTexture; texture != nil {
			if exportedTextures {
				newMat.Texture = images[*doc.Textures[texture.Index].Source]
			} else {
				newMat.TexturePath = doc.Images[*doc.Textures[texture.Index].Source].URI
			}
		}

		if gltfMat.Extras != nil {
			if dataMap, isMap := gltfMat.Extras.(map[string]interface{}); isMap {

				if c, exists := dataMap["t3dMaterialColor__"]; exists {
					color := c.([]interface{})
					newMat.Color.R = float32(color[0].(float64))
					newMat.Color.G = float32(color[1].(float64))
					newMat.Color.B = float32(color[2].(float64))
					newMat.Color.A = float32(color[3].(float64))
				}

				if s, exists := dataMap["t3dMaterialShadeless__"]; exists {
					newMat.Shadeless = s.(float64) > 0.5
				}

				if s, exists := dataMap["t3dCompositeMode__"]; exists {
					switch int(s.(float64)) {
					case 0:
						newMat.CompositeMode = ebiten.CompositeModeSourceOver
					case 1:
						newMat.CompositeMode = ebiten.CompositeModeLighter
					// case 2:
					// 	newMat.CompositeMode = ebiten.CompositeModeMultiply // Multiply doesn't work right currently
					case 3:
						newMat.CompositeMode = ebiten.CompositeModeDestinationOut
						// newMat.CompositeMode = ebiten.CompositeModeClear
					}

				}

				if s, exists := dataMap["t3dBillboardMode__"]; exists {
					switch int(s.(float64)) {
					case 0:
						newMat.BillboardMode = BillboardModeNone
					case 1:
						newMat.BillboardMode = BillboardModeXZ
					case 2:
						newMat.BillboardMode = BillboardModeAll
					}

				}

				for tagName, data := range dataMap {
					if !strings.HasPrefix(tagName, "t3d") || !strings.HasSuffix(tagName, "__") {
						newMat.Tags.Set(tagName, data)
					}
				}
			}
		}

		// If it's not exported through the Tetra addon, then just load the default GLTF material color value
		if !t3dExport {
			color := gltfMat.PBRMetallicRoughness.BaseColorFactor
			newMat.Color.R = float32(color[0])
			newMat.Color.G = float32(color[1])
			newMat.Color.B = float32(color[2])
			newMat.Color.A = float32(color[3])
		}

		newMat.Color.ConvertTosRGB()

		if gltfMat.AlphaMode == gltf.AlphaOpaque {
			if gltfLoadOptions.DefaultToAutoTransparency {
				newMat.TransparencyMode = TransparencyModeAuto
			} else {
				newMat.TransparencyMode = TransparencyModeOpaque
			}
		} else if gltfMat.AlphaMode == gltf.AlphaBlend {
			newMat.TransparencyMode = TransparencyModeTransparent
		} else { //if gltfMat.AlphaMode == gltf.AlphaMask
			newMat.TransparencyMode = TransparencyModeAlphaClip
		}

		library.Materials[gltfMat.Name] = newMat

	}

	for _, mesh := range doc.Meshes {

		newMesh := NewMesh(mesh.Name)
		library.Meshes[mesh.Name] = newMesh
		newMesh.library = library

		if mesh.Extras != nil {

			if dataMap, isMap := mesh.Extras.(map[string]interface{}); isMap {

				if vcNames, exists := dataMap["t3dVertexColorNames__"]; exists {
					for index, name := range vcNames.([]interface{}) {
						newMesh.VertexColorChannelNames[name.(string)] = index
					}
				}

				for tagName, data := range dataMap {
					if !strings.HasPrefix(tagName, "t3d") || !strings.HasSuffix(tagName, "__") {
						newMesh.Tags.Set(tagName, data)
					}
				}

			}

		}

		for _, v := range mesh.Primitives {

			posBuffer := [][3]float32{}
			vertPos, err := modeler.ReadPosition(doc, doc.Accessors[v.Attributes[gltf.POSITION]], posBuffer)

			if err != nil {
				return nil, err
			}

			vertexData := make([]VertexInfo, len(vertPos))

			for i, v := range vertPos {

				vertexData[i] = NewVertex(
					float64(v[0]),
					float64(v[1]),
					float64(v[2]),
					0, 0,
				)

			}

			if texCoordAccessor, texCoordExists := v.Attributes[gltf.TEXCOORD_0]; texCoordExists {

				uvBuffer := [][2]float32{}

				texCoords, err := modeler.ReadTextureCoord(doc, doc.Accessors[texCoordAccessor], uvBuffer)

				if err != nil {
					return nil, err
				}

				for i, v := range texCoords {
					vertexData[i].U = float64(v[0])
					vertexData[i].V = -(float64(v[1]) - 1)
				}

			}

			if normalAccessor, normalExists := v.Attributes[gltf.NORMAL]; normalExists {

				normalBuffer := [][3]float32{}

				normals, err := modeler.ReadNormal(doc, doc.Accessors[normalAccessor], normalBuffer)

				if err != nil {
					return nil, err
				}

				for i, v := range normals {
					vertexData[i].NormalX = float64(v[0])
					vertexData[i].NormalY = float64(v[1])
					vertexData[i].NormalZ = float64(v[2])
				}

			}

			// Blender max is 8 vertex color channels
			for colorChannelIndex := 0; colorChannelIndex < 8; colorChannelIndex++ {

				colorChannelName := "COLOR_" + strconv.Itoa(colorChannelIndex)

				if vertexColorAccessor, vcExists := v.Attributes[colorChannelName]; vcExists {

					vcBuffer := [][4]uint16{}

					colors, err := modeler.ReadColor64(doc, doc.Accessors[vertexColorAccessor], vcBuffer)

					if err != nil {
						return nil, err
					}

					for i, colorData := range colors {

						// colors are exported from Blender as linear, but display as sRGB so we'll convert them here
						color := NewColor(
							float32(colorData[0])/math.MaxUint16,
							float32(colorData[1])/math.MaxUint16,
							float32(colorData[2])/math.MaxUint16,
							float32(colorData[3])/math.MaxUint16,
						)
						color.ConvertTosRGB()
						vertexData[i].Colors = append(vertexData[i].Colors, color)
						vertexData[i].ActiveColorChannel = 0

					}

				} else {
					break
				}

			}

			if weightAccessor, weightExists := v.Attributes[gltf.WEIGHTS_0]; weightExists {

				weightBuffer := [][4]float32{}
				weights, err := modeler.ReadWeights(doc, doc.Accessors[weightAccessor], weightBuffer)

				if err != nil {
					return nil, err
				}

				boneBuffer := [][4]uint16{}
				bones, err := modeler.ReadJoints(doc, doc.Accessors[v.Attributes[gltf.JOINTS_0]], boneBuffer)

				if err != nil {
					return nil, err
				}

				// Store weights and bones; we don't want to waste space and speed storing bones if their weights are 0
				for w := range weights {
					vWeights := weights[w]
					for i := range vWeights {
						if vWeights[i] > 0 {
							vertexData[w].Weights = append(vertexData[w].Weights, vWeights[i])
							vertexData[w].Bones = append(vertexData[w].Bones, bones[w][i])
						}
					}
				}

			}

			indexBuffer := []uint32{}

			indices, err := modeler.ReadIndices(doc, doc.Accessors[*v.Indices], indexBuffer)

			if err != nil {
				return nil, err
			}

			newVerts := make([]VertexInfo, len(indices))

			for i := 0; i < len(indices); i++ {
				newVerts[i] = vertexData[indices[i]]
			}

			var mat *Material

			if v.Material != nil {
				gltfMat := doc.Materials[*v.Material]
				mat = library.Materials[gltfMat.Name]
			}

			mp := newMesh.AddMeshPart(mat)

			mp.AddTriangles(newVerts...)

			newMesh.UpdateBounds()

		}

	}

	for _, gltfAnim := range doc.Animations {
		anim := NewAnimation(gltfAnim.Name)
		anim.library = library
		library.Animations[gltfAnim.Name] = anim

		animLength := 0.0

		for _, channel := range gltfAnim.Channels {

			sampler := gltfAnim.Samplers[*channel.Sampler]

			channelName := "root"
			if channel.Target.Node != nil {
				channelName = doc.Nodes[*channel.Target.Node].Name
			}

			animChannel := anim.Channels[channelName]
			if animChannel == nil {
				animChannel = anim.AddChannel(channelName)
			}

			if channel.Target.Path == gltf.TRSTranslation {

				id, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Input], nil)

				if err != nil {
					return nil, err
				}

				inputData := id.([]float32)

				od, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Output], nil)

				if err != nil {
					return nil, err
				}

				outputData := od.([][3]float32)

				track := animChannel.AddTrack(TrackTypePosition)
				track.Interpolation = int(sampler.Interpolation)
				for i := 0; i < len(inputData); i++ {
					t := inputData[i]
					p := outputData[i]
					track.AddKeyframe(float64(t), vector.Vector{float64(p[0]), float64(p[1]), float64(p[2])})
					if float64(t) > animLength {
						animLength = float64(t)
					}
				}

			} else if channel.Target.Path == gltf.TRSScale {

				id, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Input], nil)

				if err != nil {
					return nil, err
				}

				inputData := id.([]float32)

				od, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Output], nil)

				if err != nil {
					return nil, err
				}

				outputData := od.([][3]float32)

				track := animChannel.AddTrack(TrackTypeScale)
				track.Interpolation = int(sampler.Interpolation)
				for i := 0; i < len(inputData); i++ {
					t := inputData[i]
					p := outputData[i]
					track.AddKeyframe(float64(t), vector.Vector{float64(p[0]), float64(p[1]), float64(p[2])})
					if float64(t) > animLength {
						animLength = float64(t)
					}
				}

			} else if channel.Target.Path == gltf.TRSRotation {

				id, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Input], nil)

				if err != nil {
					return nil, err
				}

				inputData := id.([]float32)

				od, err := modeler.ReadAccessor(doc, doc.Accessors[*sampler.Output], nil)

				if err != nil {
					return nil, err
				}

				outputData := od.([][4]float32)

				track := animChannel.AddTrack(TrackTypeRotation)
				track.Interpolation = int(sampler.Interpolation)

				for i := 0; i < len(inputData); i++ {
					t := inputData[i]
					p := outputData[i]
					track.AddKeyframe(float64(t), NewQuaternion(float64(p[0]), float64(p[1]), float64(p[2]), float64(p[3])))
					if float64(t) > animLength {
						animLength = float64(t)
					}
				}

			}

		}

		if gltfAnim.Extras != nil {
			m := gltfAnim.Extras.(map[string]interface{})
			if markerData, exists := m["t3dMarkers__"]; exists {
				for _, mData := range markerData.([]interface{}) {

					marker := mData.(map[string]interface{})

					anim.Markers = append(anim.Markers, Marker{
						Name: marker["name"].(string),
						Time: marker["time"].(float64),
					})
				}
			}
		}

		anim.Length = animLength

	}

	// skins := []*Skin{}

	// for _, skin := range doc.Skins {

	// 	skins = append(skins, skin.)

	// }

	objects := []INode{}

	objToNode := map[INode]*gltf.Node{}

	nodeHasProp := func(node *gltf.Node, propName string) bool {

		if node.Extras == nil {
			return false
		}

		if _, exists := node.Extras.(map[string]interface{})[propName]; exists {
			return true
		}
		return false

	}

	for _, node := range doc.Nodes {

		var obj INode

		if node.Mesh != nil {
			mesh := library.Meshes[doc.Meshes[*node.Mesh].Name]
			obj = NewModel(mesh, node.Name)
		} else if node.Camera != nil {

			gltfCam := doc.Cameras[*node.Camera]

			newCam := NewCamera(gltfLoadOptions.CameraWidth, gltfLoadOptions.CameraHeight)
			newCam.name = node.Name
			newCam.RenderDepth = gltfLoadOptions.CameraDepth

			if gltfCam.Perspective != nil {
				newCam.Near = float64(gltfCam.Perspective.Znear)
				newCam.Far = float64(*gltfCam.Perspective.Zfar)
				newCam.FieldOfView = float64(gltfCam.Perspective.Yfov) / (math.Pi * 2) * 360
				newCam.Perspective = true
			} else if gltfCam.Orthographic != nil {
				newCam.Near = float64(gltfCam.Orthographic.Znear)
				newCam.Far = float64(gltfCam.Orthographic.Zfar)
				newCam.OrthoScale = float64(gltfCam.Orthographic.Xmag)
				newCam.Perspective = false
			}

			obj = newCam

		} else if lighting := node.Extensions["KHR_lights_punctual"]; lighting != nil {
			lights := doc.Extensions["KHR_lights_punctual"].(lightspuntual.Lights)
			lightData := lights[lighting.(lightspuntual.LightIndex)]

			if lightData.Type == lightspuntual.TypeDirectional {
				directionalLight := NewDirectionalLight(node.Name, lightData.Color[0], lightData.Color[1], lightData.Color[2], *lightData.Intensity)
				obj = directionalLight
			} else if lightData.Type == lightspuntual.TypePoint {
				pointLight := NewPointLight(node.Name, lightData.Color[0], lightData.Color[1], lightData.Color[2], *lightData.Intensity/1000)
				if !math.IsInf(float64(*lightData.Range), 0) {
					pointLight.Distance = float64(*lightData.Range)
				}
				obj = pointLight
			} else {
				// Any unsupported light type just gets turned into an ambient light
				pointLight := NewAmbientLight(node.Name, lightData.Color[0], lightData.Color[1], lightData.Color[2], *lightData.Intensity/1000)
				obj = pointLight
			}

		} else if node.Extras != nil && nodeHasProp(node, "t3dPathPoints__") {

			points := []vector.Vector{}
			extraMap := node.Extras.(map[string]interface{})

			for _, p := range extraMap["t3dPathPoints__"].([]interface{}) {
				pointData := p.([]interface{})
				points = append(points, vector.Vector{pointData[0].(float64), pointData[2].(float64), -pointData[1].(float64)})
			}

			path := NewPath(node.Name, points...)

			if nodeHasProp(node, "t3dPathCyclic__") {
				path.Closed = extraMap["t3dPathCyclic__"].(float64) > 0
			}

			obj = path

		} else {
			obj = NewNode(node.Name)
		}

		objToNode[obj] = node

		obj.setLibrary(library)

		for _, child := range node.Children {
			obj.AddChildren(objects[int(child)])
		}

		if node.Extras != nil {
			if dataMap, isMap := node.Extras.(map[string]interface{}); isMap {

				getOrDefaultBool := func(path string, defaultValue bool) bool {
					if value, exists := dataMap[path]; exists {
						return value.(float64) > 0.5
					}
					return defaultValue
				}

				getOrDefaultFloat := func(path string, defaultValue float64) float64 {
					if value, exists := dataMap[path]; exists {
						return value.(float64)
					}
					return defaultValue
				}

				getOrDefaultFloatSlice := func(path string, defaultValues []float64) []float64 {
					if value, exists := dataMap[path]; exists {
						floats := []float64{}
						for _, v := range value.([]interface{}) {
							floats = append(floats, v.(float64))
						}
						return floats
					}
					return defaultValues
				}

				getOrDefaultVector3D := func(path string, defaultX, defaultY, defaultZ float64) vector.Vector {
					if value, exists := dataMap[path]; exists {
						floats := []float64{}
						for _, v := range value.([]interface{}) {
							floats = append(floats, v.(float64))
						}
						return vector.Vector{floats[0], floats[2], -floats[1]}
					}
					return vector.Vector{defaultX, defaultY, defaultZ}
				}

				obj.setOriginalLocalPosition(getOrDefaultVector3D("t3dOriginalLocalPosition__", 0, 0, 0))

				obj.SetVisible(getOrDefaultBool("t3dVisible__", true), false)

				if bt, exists := dataMap["t3dBoundsType__"]; exists {

					boundsType := int(bt.(float64))

					switch boundsType {
					// case 0: // NONE
					case 1: // AABB

						var aabb *BoundingAABB

						if aabbCustomEnabled := getOrDefaultBool("t3dAABBCustomEnabled__", false); aabbCustomEnabled {

							boundsSize := getOrDefaultFloatSlice("t3dAABBCustomSize__", []float64{2, 2, 2})
							aabb = NewBoundingAABB("_bounding aabb", boundsSize[0], boundsSize[1], boundsSize[2])

						} else if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
							mesh := obj.(*Model).Mesh
							dim := mesh.Dimensions
							aabb = NewBoundingAABB("_bounding aabb", dim.Width(), dim.Height(), dim.Depth())
						}

						if aabb != nil {

							if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
								aabb.SetLocalPosition(obj.(*Model).Mesh.Dimensions.Center())
							}

							obj.AddChildren(aabb)

						} else {
							log.Println("Warning: object " + obj.Name() + " has bounds type BoundingAABB with no size and is not a Model")
						}

					case 2: // Capsule

						var capsule *BoundingCapsule

						if capsuleCustomEnabled := getOrDefaultBool("t3dCapsuleCustomEnabled__", false); capsuleCustomEnabled {
							height := getOrDefaultFloat("t3dCapsuleCustomHeight__", 2)
							radius := getOrDefaultFloat("t3dCapsuleCustomRadius__", 0.5)
							capsule = NewBoundingCapsule("_bounding capsule", height, radius)
						} else if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
							mesh := obj.(*Model).Mesh
							dim := mesh.Dimensions
							capsule = NewBoundingCapsule("_bounding capsule", dim.Height(), math.Max(dim.Width(), dim.Depth())/2)
						}

						if capsule != nil {

							if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
								capsule.SetLocalPosition(obj.(*Model).Mesh.Dimensions.Center())
							}

							obj.AddChildren(capsule)

						} else {
							log.Println("Warning: object " + obj.Name() + " has bounds type BoundingCapsule with no size and is not a Model")
						}

					case 3: // Sphere

						var sphere *BoundingSphere

						if sphereCustomEnabled := getOrDefaultBool("t3dSphereCustomEnabled__", false); sphereCustomEnabled {
							radius := getOrDefaultFloat("t3dSphereCustomRadius__", 1)
							sphere = NewBoundingSphere("_bounding sphere", radius)
						} else if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {

							model := obj.(*Model)
							dim := model.Mesh.Dimensions.Clone()
							scale := model.WorldScale()
							dim[0][0] *= scale[0]
							dim[0][1] *= scale[1]
							dim[0][2] *= scale[2]

							dim[1][0] *= scale[0]
							dim[1][1] *= scale[1]
							dim[1][2] *= scale[2]

							sphere = NewBoundingSphere("_bounding sphere", dim.MaxDimension()/2)
						}

						if sphere != nil {

							if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
								sphere.SetLocalPosition(obj.(*Model).Mesh.Dimensions.Center())
							}

							obj.AddChildren(sphere)

						} else {
							log.Println("Warning: object " + obj.Name() + " has bounds type BoundingSphere with no size and is not a Model")
						}

					case 4: // Triangles

						if obj.Type().Is(NodeTypeModel) && obj.(*Model).Mesh != nil {
							triangles := NewBoundingTriangles("_bounding triangles", obj.(*Model).Mesh)
							obj.AddChildren(triangles)
						}

					}
				}

				for tagName, data := range dataMap {
					if !strings.HasPrefix(tagName, "t3d") || !strings.HasSuffix(tagName, "__") {
						obj.Tags().Set(tagName, data)
					}
				}
			}
		}

		mtData := node.Matrix

		matrix := NewMatrix4()
		matrix = matrix.SetRow(0, vector.Vector{float64(mtData[0]), float64(mtData[1]), float64(mtData[2]), float64(mtData[3])})
		matrix = matrix.SetRow(1, vector.Vector{float64(mtData[4]), float64(mtData[5]), float64(mtData[6]), float64(mtData[7])})
		matrix = matrix.SetRow(2, vector.Vector{float64(mtData[8]), float64(mtData[9]), float64(mtData[10]), float64(mtData[11])})
		matrix = matrix.SetRow(3, vector.Vector{float64(mtData[12]), float64(mtData[13]), float64(mtData[14]), float64(mtData[15])})

		if !matrix.IsIdentity() {

			p, s, r := matrix.Decompose()

			obj.SetLocalPosition(p)
			obj.SetLocalScale(s)
			obj.SetLocalRotation(r)

		} else {

			obj.SetLocalPosition(vector.Vector{float64(node.Translation[0]), float64(node.Translation[1]), float64(node.Translation[2])})
			obj.SetLocalScale(vector.Vector{float64(node.Scale[0]), float64(node.Scale[1]), float64(node.Scale[2])})
			obj.SetLocalRotation(NewMatrix4RotateFromQuaternion(NewQuaternion(float64(node.Rotation[0]), float64(node.Rotation[1]), float64(node.Rotation[2]), float64(node.Rotation[3]))))

		}

		objects = append(objects, obj)

	}

	allBones := []*Node{}

	// We do this again here so we can be sure that all of the nodes can be created first
	for i, node := range doc.Nodes {

		// Set up skin for skinning animations
		if node.Skin != nil {

			model := objects[i].(*Model)

			skin := doc.Skins[*node.Skin]

			// Unsure of if this is necessary.
			// if skin.Skeleton != nil {
			// 	skeletonRoot := objects[*skin.Skeleton]
			// 	model.SetWorldPosition(skeletonRoot.WorldPosition())
			// 	model.SetWorldScale(skeletonRoot.WorldScale())
			// 	model.SetWorldRotation(skeletonRoot.WorldRotation())
			// }

			model.Skinned = true

			// We should keep a local slice of bones because we can't simply loop through all bones with the matrix index, as
			// the matrix index resets at 0 for each armature, naturally.
			localBones := []*Node{}

			for _, b := range skin.Joints {
				bone := objects[b].(*Node)
				// This is incorrect, but it gives us a link to any bone in the armature to establish
				// the true root after parenting is set below
				model.SkinRoot = bone
				allBones = append(allBones, bone)
				localBones = append(localBones, bone)
			}

			matrices, err := modeler.ReadAccessor(doc, doc.Accessors[*skin.InverseBindMatrices], nil)
			if err != nil {
				return nil, err
			}

			for matIndex, matrix := range matrices.([][4][4]float32) {

				newMat := NewMatrix4()
				for rowIndex, row := range matrix {
					newMat = newMat.SetColumn(rowIndex, vector.Vector{float64(row[0]), float64(row[1]), float64(row[2]), float64(row[3])})
				}

				localBones[matIndex].inverseBindMatrix = newMat
				localBones[matIndex].isBone = true

			}

			for vertIndex, boneIndices := range model.Mesh.VertexBones {
				model.bones = append(model.bones, []*Node{})

				for _, boneID := range boneIndices {
					model.bones[vertIndex] = append(model.bones[vertIndex], allBones[boneID])
				}

				// model.Mesh.VertexBones[i] = append(model.Mesh.VertexBones[i], )
			}

			// for _, part := range model.Mesh.MeshParts {

			// 	for _, vertex := range part.Vertices {

			// 		model.bones = append(model.bones, []*Node{})

			// 		for _, boneID := range verticesToBones[vertex] {
			// 			model.bones[vertex.ID] = append(model.bones[vertex.ID], allBones[boneID])
			// 		}

			// 	}

			// }

		}

		// Set up parenting
		for _, childIndex := range node.Children {
			objects[i].AddChildren(objects[int(childIndex)])
		}

	}

	getOrDefaultInt := func(propMap map[string]interface{}, key string, defaultValue int) int {
		if value, keyExists := propMap[key]; keyExists {
			return int(value.(float64))
		}
		return defaultValue
	}

	getOrDefaultString := func(propMap map[string]interface{}, key string, defaultValue string) string {
		if value, keyExists := propMap[key]; keyExists {
			return value.(string)
		}
		return defaultValue
	}

	getOrDefaultFloat := func(propMap map[string]interface{}, key string, defaultValue float64) float64 {
		if value, keyExists := propMap[key]; keyExists {
			return value.(float64)
		}
		return defaultValue
	}

	getOrDefaultBool := func(propMap map[string]interface{}, key string, defaultValue bool) bool {
		if value, keyExists := propMap[key]; keyExists {
			return value.(float64) > 0
		}
		return defaultValue
	}

	getIfExistingMap := func(propMap map[string]interface{}, key string) map[string]interface{} {
		if value, keyExists := propMap[key]; keyExists && value != nil {
			return value.(map[string]interface{})
		}
		return nil
	}

	findNode := func(objName string) INode {
		for _, obj := range objects {
			if obj.Name() == objName {
				return obj
			}
		}
		return nil
	}

	// At this point, parenting should be set up.
	for obj, node := range objToNode {

		if node.Extras != nil {
			if dataMap, isMap := node.Extras.(map[string]interface{}); isMap {

				if gameProps, exists := dataMap["t3dGameProperties__"]; exists {
					for _, p := range gameProps.([]interface{}) {

						property := p.(map[string]interface{})

						propType := getOrDefaultInt(property, "valueType", 0)

						// Property types:

						// bool, int, float, string, reference (string)

						name := getOrDefaultString(property, "name", "New Property")
						var value interface{}

						if propType == 0 {
							value = getOrDefaultBool(property, "valueBool", false)
						} else if propType == 1 {
							value = getOrDefaultInt(property, "valueInt", 0)
						} else if propType == 2 {
							value = getOrDefaultFloat(property, "valueFloat", 0)
						} else if propType == 3 {
							value = getOrDefaultString(property, "valueString", "")
						} else if propType == 4 {
							scene := ""
							// Can be nil if it was set to something and then set to nothing
							if ref := getIfExistingMap(property, "valueReferenceScene"); ref != nil {
								scene = getOrDefaultString(ref, "name", "")
							}
							if ref := getIfExistingMap(property, "valueReference"); ref != nil {
								value = scene + ":" + getOrDefaultString(ref, "name", "")
							}
						}

						obj.Tags().Set(name, value)

					}
				}

			}

		}
	}

	// Set up SkinRoot for skinned Models; this should be the root node of a hierarchy of bone Nodes.
	for _, n := range objects {

		if model, isModel := n.(*Model); isModel && model.Skinned {

			parent := model.SkinRoot

			for parent.IsBone() && parent.Parent() != nil {
				parent = parent.Parent()
			}

			model.SkinRoot = parent

		}

	}

	for obj, node := range objToNode {

		if node.Extras != nil {
			if dataMap, isMap := node.Extras.(map[string]interface{}); isMap {

				if c, exists := dataMap["t3dInstanceCollection__"]; exists {
					collection := collections[c.(string)]

					offset := vector.Vector{-collection.Offset[0], -collection.Offset[2], collection.Offset[1]}

					for _, cloneName := range collection.Objects {

						var clone INode

						path := collection.Path

						if path == "" {
							clone = findNode(cloneName).Clone()
						} else {
							path = strings.ReplaceAll(path, "//", "") // Blender relative paths have double-slashes; we don't need them to

							if gltfLoadOptions.DependentLibraryResolver == nil {
								panic("Error in instantiating linked element " + cloneName + " as the Dependent Library Resolver function is nil.")
							}

							if library := gltfLoadOptions.DependentLibraryResolver(path); library != nil {
								if foundNode := library.FindNode(cloneName); foundNode != nil {
									clone = foundNode.Clone()
								}
							}
						}

						if clone != nil {

							clone.MoveVec(offset)
							obj.AddChildren(clone)

						} else {
							log.Println("Error in instantiating linked element:", cloneName, "from:", path, "; did you pass the Library as a dependent Library in the GLTFLoadOptions struct?")
						}

					}

				}

			}

		}
	}

	for _, s := range doc.Scenes {

		scene := library.AddScene(s.Name)

		scene.library = library

		for _, n := range s.Nodes {
			scene.Root.AddChildren(objects[n])
		}

		if s.Extras != nil {
			dataMap := s.Extras.(map[string]interface{})
			if wc, exists := dataMap["t3dWorldColor__"]; exists {
				wcc := wc.([]interface{})
				worldColor := NewColor(float32(wcc[0].(float64)), float32(wcc[1].(float64)), float32(wcc[2].(float64)), 1)
				worldColor.ConvertTosRGB()
				ambientLight := NewAmbientLight("World Ambient", 1, 1, 1, float32(dataMap["t3dWorldEnergy__"].(float64)))
				ambientLight.Color = worldColor
				scene.Root.AddChildren(ambientLight)
			}

			if cc, exists := dataMap["t3dClearColor__"]; exists {
				wcc := cc.([]interface{})
				clearColor := NewColor(float32(wcc[0].(float64)), float32(wcc[1].(float64)), float32(wcc[2].(float64)), float32(wcc[3].(float64)))
				clearColor.ConvertTosRGB()
				scene.ClearColor = clearColor
			}

			if v, exists := dataMap["t3dFogMode__"]; exists {
				fm := v.(string)
				switch fm {
				case "OFF":
					scene.FogMode = FogOff
				case "ADDITIVE":
					scene.FogMode = FogAdd
				case "MULTIPLY":
					scene.FogMode = FogMultiply
				case "OVERWRITE":
					scene.FogMode = FogOverwrite
				}
			}

			if v, exists := dataMap["t3dFogColor__"]; exists {
				wcc := v.([]interface{})
				fogColor := NewColor(float32(wcc[0].(float64)), float32(wcc[1].(float64)), float32(wcc[2].(float64)), float32(wcc[3].(float64)))
				fogColor.ConvertTosRGB()
				scene.FogColor = fogColor
			}

			if v, exists := dataMap["t3dFogRangeStart__"]; exists {
				fogStart := v.(float64)
				scene.FogRange[0] = float32(fogStart)
			}

			if v, exists := dataMap["t3dFogRangeEnd__"]; exists {
				fogEnd := v.(float64)
				scene.FogRange[1] = float32(fogEnd)
			}

		}

	}

	// Cameras exported through GLTF become nodes + a camera child with the correct orientation for some reason???
	// So here we basically cut the empty nodes out of the equation, leaving just the cameras with the correct orientation.

	// EDIT: This is no longer done, as the camera direction in Blender and the camera direction in GLTF aren't the same, whoops.
	// See: https://github.com/KhronosGroup/glTF-Blender-Exporter/issues/113
	// Cutting out the inserted correction Node breaks relative transforms (i.e. camera parented to another object for positioning).

	// for _, n := range objects {

	// 	if camera, isCamera := n.(*Camera); isCamera {
	// 		oldParent := camera.Parent()
	// 		root := oldParent.Parent()

	// 		camera.name = oldParent.Name()

	// 		for _, child := range oldParent.Children() {
	// 			if child == camera {
	// 				continue
	// 			}
	// 			camera.AddChildren(child)
	// 		}

	// 		root.RemoveChildren(camera.parent)
	// 		root.AddChildren(camera)
	// 	}

	// }

	library.ExportedScene = library.Scenes[*doc.Scene]

	return library, nil

}
